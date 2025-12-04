package netquic

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"time"

	"envelop/envelop"
	"envelop/frame"
	"envelop/peer"

	quic "github.com/quic-go/quic-go"
)

///////////////////////////////////////////////////////////////////////////////
// PeerManager：负责“怎么把 Envelope 发给某个 PeerID”
///////////////////////////////////////////////////////////////////////////////
//
// 职责：
//   1. 通过 resolver(peerID) 拿到该节点的所有候选地址（IPv6 / IPv4 / 多端口）
//   2. 为每个地址维护并复用 QUIC 连接（addr → quic.Conn）
//   3. SendToPeer：自动选地址（按优先级顺序），建立或复用 QUIC 连接，
//      打开单向流，构造 Frame（Frame v2），写入 Envelope 数据。
//
// 注意：
//   - PeerManager 不关心 Envelope 解析，它只把“原始 Envelope 字节”包装进 Frame。
//   - 多地址支持通过 resolver 返回 []string 实现。
//   - 你在 RelayRegistry 里可以把 IPv6/IPv4/内网/公网地址都放进去，
//     resolver 按优先级顺序返回即可。
///////////////////////////////////////////////////////////////////////////////

type PeerManager struct {
	mu    sync.Mutex
	conns map[string]*quic.Conn

	// resolve: PeerID → 候选地址列表（按优先级排序）
	//
	// 例如：
	//   func(id PeerID) []string {
	//       return []string{
	//           "[2001:db8::1]:9001", // IPv6 优先
	//           "203.0.113.1:9001",   // IPv4 备用
	//       }
	//   }
	resolve func(peer.PeerID) []string

	tlsConf  *tls.Config
	quicConf *quic.Config
}

// NewPeerManager 创建一个 PeerManager。
// 这里的 resolver 由上层注入——通常来自 RelayRegistry。
func NewPeerManager(resolver func(peer.PeerID) []string) *PeerManager {
	return &PeerManager{
		conns:   make(map[string]*quic.Conn),
		resolve: resolver,
		tlsConf: generateTLSConfig(), // 直接复用 node.go 里的 TLS 生成函数
		quicConf: &quic.Config{
			EnableDatagrams: true,
			MaxIdleTimeout:  time.Minute * 3,
		},
	}
}

///////////////////////////////////////////////////////////////////////////////
// 1. getConn：给定一个地址，拿到一个 QUIC 连接
//
//   - 如果连接池里已经有存活的 conn，则直接复用
//   - 如果没有，则新建一个 QUIC 连接（UDP + Dial）
//   - 最终返回一个可用的 *quic.Conn
///////////////////////////////////////////////////////////////////////////////

func (pm *PeerManager) getConn(addr string) (*quic.Conn, error) {
	pm.mu.Lock()
	conn := pm.conns[addr]
	if conn != nil && conn.Context().Err() == nil {
		// 连接存在且上下文没报错（未关闭），直接复用
		pm.mu.Unlock()
		return conn, nil
	}
	pm.mu.Unlock()

	// 需要新建连接
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}

	// 本地随机端口
	udpConn, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, err
	}

	newConn, err := quic.Dial(
		context.Background(),
		udpConn,
		udpAddr,
		pm.tlsConf,
		pm.quicConf,
	)
	if err != nil {
		return nil, err
	}

	// 放入连接池
	pm.mu.Lock()
	pm.conns[addr] = newConn
	pm.mu.Unlock()

	return newConn, nil
}

///////////////////////////////////////////////////////////////////////////////
// 2. SendToPeer：根据 PeerID 发送一个 Envelope
//
//   流程：
//     1）通过 resolve(peerID) 拿到地址列表（可能有 IPv6 / IPv4）
//     2）按顺序逐个尝试：
//           - getConn(addr)
//           - 打开单向流 conn.OpenUniStream()
//           - Envelope.Marshal() → []byte
//           - Frame.Build(FrameTypeNormal, envBytes, 0)
//           - stream.Write(frame.Raw)
//        一旦某个地址发送成功，立即返回 nil
//     3）如果所有地址都失败，返回最后一个错误
//
//   这样，你在上层只需要关心 “我要发给 peerID X”，至于：
//     - 走 IPv6 还是 IPv4
//     - 某个地址暂时连不上怎么办
//   都交给 PeerManager 自动处理。
///////////////////////////////////////////////////////////////////////////////

func (pm *PeerManager) SendToPeer(id peer.PeerID, env *envelop.Envelope) error {
	// 1. 通过 resolver 获取候选地址列表
	addrs := pm.resolve(id)
	if len(addrs) == 0 {
		return fmt.Errorf("no address for peer %s", peer.PeerIDToDomain(id))
	}

	var lastErr error

	// 2. 按顺序逐个尝试（典型策略：IPv6 → IPv4 → 内网 → 其它）
	for _, addr := range addrs {
		// 2.1 拿到（或新建）QUIC 连接
		conn, err := pm.getConn(addr)
		if err != nil {
			lastErr = fmt.Errorf("dial %s failed: %w", addr, err)
			continue
		}

		// 2.2 打开一个单向流
		stream, err := conn.OpenUniStream()
		if err != nil {
			lastErr = fmt.Errorf("open stream to %s failed: %w", addr, err)
			continue
		}

		// 2.3 Envelope → 原始字节（严格按照 EnvHeaderSize 布局）
		envBytes, err := envelop.Marshal(env)
		if err != nil {
			lastErr = fmt.Errorf("envelop envelop failed: %w", err)
		}
		// 2.4 构建 Frame（这里用 FrameTypeNormal，可变大小，不 padding）
		f := &frame.Frame{}
		if err := f.Build(frame.FrameTypeNormal, envBytes, 0); err != nil {
			lastErr = fmt.Errorf("build frame for %s failed: %w", addr, err)
			continue
		}

		// 2.5 写入 QUIC 流
		if _, err := stream.Write(f.Raw); err != nil {
			lastErr = fmt.Errorf("write frame to %s failed: %w", addr, err)
			continue
		}
		// ⭐⭐ 2.6 非常关键：写完一定要告诉对端“这条消息结束了”
		//     对端的 io.ReadAll(stream) 才会返回 EOF
		if err := stream.Close(); err != nil {
			lastErr = fmt.Errorf("close stream to %s failed: %w", addr, err)
			continue
		}
		// 2.7 这条地址成功了，直接返回
		return nil
	}

	// 3. 所有地址都失败，返回最后一个错误
	if lastErr == nil {
		lastErr = fmt.Errorf("send failed: unknown error for peer %s", peer.PeerIDToDomain(id))
	}
	return lastErr
}
