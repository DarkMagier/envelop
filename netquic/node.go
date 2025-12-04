package netquic

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"envelop/envelop"
	"envelop/frame"
	"envelop/peer"
	"envelop/router"

	"io"
	"log"
	"math/big"
	"net"
	"time"

	quic "github.com/quic-go/quic-go"
)

/*
==========================================================
 Node：网络层的「节点」抽象
==========================================================

职责（只做“网络 → Envelope”这一半）：

1. 启动 QUIC 监听（ListenAndServe）
2. 接受新连接（handleConn）
3. 在连接上接受「单向流」(AcceptUniStream)
4. 从流里读出一整块 Frame 原始字节
5. Frame.Decode → 拿到 Envelope 的二进制
6. envelop.Unmarshal → 恢复 Envelope 结构
7. 做一些通用控制逻辑：
    - 如果是 REGISTER（Flags=1） → 调 OnRegisterPeer
    - 如果需要做路由学习 → 调 OnEnvelope(from, env)
8. 最后交给上层 Router.HandleEnvelope(env)

注意：
- Node 不关心业务（InnerPayload 是什么不管）
- Node 不做 RPC，不做 JSON，只做 Envelope
- 所有「我要把包发给谁」的问题交给 PeerManager
*/

type Node struct {
	Name string        // 节点名字，方便日志
	Key  *peer.KeyPair // 节点本地身份（仅用于 PeerID，不参与 TLS）

	Router   *router.Router // Envelope 层路由逻辑
	PeerMgr  *PeerManager   // 主动发包：PeerID → QUIC Connection
	Registry *RelayRegistry // 可选：用于 addr → PeerID 的反查（路由学习）

	// 当收到 REGISTER 信封（Flags=1）时调用：
	//   - id   = 对方的 PeerID（从 env.ReturnPeerID 里来）
	//   - addr = conn.RemoteAddr().String() 看到的远端地址
	OnRegisterPeer func(id peer.PeerID, addr string)

	// 当收到普通 Envelope 时，如果你想做「多跳路由学习」，
	// 可以在这里把 from / env.ReturnPeerID 写入 RouteTable。
	//
	//   from = 通过 Registry.PeerByAddr(remoteAddr) 反查出来的 PeerID。
	OnEnvelope func(from peer.PeerID, env *envelop.Envelope)
}

///////////////////////////////////////////////////////////
// 1. TLS 配置：自签证书，满足 QUIC 要求即可
///////////////////////////////////////////////////////////

func generateTLSConfig() *tls.Config {
	// 生成一对 ECDSA 密钥（quic-go 官方示例也常这么搞）
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}

	// 简单自签名证书：内部 P2P 使用足够
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),

		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		panic(err)
	}

	return &tls.Config{
		InsecureSkipVerify: true,                     // 自签证书，不校验证书链
		NextProtos:         []string{"envelop-quic"}, // ALPN 协议名
		Certificates: []tls.Certificate{
			{Certificate: [][]byte{derBytes}, PrivateKey: priv},
		},
	}
}

///////////////////////////////////////////////////////////
// 2. 启动 QUIC 监听
///////////////////////////////////////////////////////////

// ListenAndServe 在指定 UDP 地址上监听 QUIC 连接。
func (n *Node) ListenAndServe(addr string) error {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return err
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return err
	}

	tlsConf := generateTLSConfig()
	quicConf := &quic.Config{
		EnableDatagrams: true,
		MaxIdleTimeout:  3 * time.Minute,
	}

	// 注意：v0.57 里 Listen 返回的是 quic.Listener，
	// Accept 返回的是 quic.Connection 接口。
	listener, err := quic.Listen(udpConn, tlsConf, quicConf)
	if err != nil {
		return err
	}

	log.Printf("[%s] QUIC Listening on %s", n.Name, addr)

	for {
		// 阻塞等待新连接
		conn, err := listener.Accept(context.Background())
		if err != nil {
			log.Printf("[%s] Accept err: %v", n.Name, err)
			continue
		}
		// 每个连接丢给 goroutine 处理
		go n.handleConn(conn)
	}
}

///////////////////////////////////////////////////////////
// 3. 处理新连接：一个 Connection 上可以有很多单向流
///////////////////////////////////////////////////////////

func (n *Node) handleConn(conn *quic.Conn) {
	log.Printf("[%s] Accepted connection from %s", n.Name, conn.RemoteAddr())

	for {
		// 接受「对方发来的单向流」
		stream, err := conn.AcceptUniStream(context.Background())
		if err != nil {
			log.Printf("[%s] AcceptUniStream err: %v", n.Name, err)
			return
		}

		// 每个流丢给一个 goroutine，顺便把 conn 传下去（为了拿 remoteAddr）
		go n.handleStream(stream, conn)
	}
}

///////////////////////////////////////////////////////////
// 4. 从单向流中读取 Frame → Envelope
///////////////////////////////////////////////////////////

// handleStream：这个函数做的事是：
//  1. 把当前 stream 的全部数据一次性读完（io.ReadAll）
//  2. Frame.Decode → envBytes
//  3. envelop.Unmarshal → Envelope
//  4. 处理 REGISTER / OnEnvelope
//  5. 把 Envelope 交给 Router.HandleEnvelope
func (n *Node) handleStream(stream *quic.ReceiveStream, conn *quic.Conn) {
	// =======================
	// 1）读取整条流
	// =======================
	data, err := io.ReadAll(stream)
	if err != nil {
		log.Printf("[%s] ReadAll err: %v", n.Name, err)
		return
	}

	// =======================
	// 2）Frame.Decode：解析出 Frame 和其中的 Envelope 字节
	// =======================
	_, envBytes, err := frame.Decode(data)
	if err != nil {
		log.Printf("[%s] Frame decode err: %v", n.Name, err)
		return
	}

	// =======================
	// 3）Envelope.Unmarshal：恢复 Envelope 结构
	// =======================
	env, err := envelop.Unmarshal(envBytes)
	if err != nil {
		log.Printf("[%s] Envelope decode err: %v", n.Name, err)
		return
	}

	// =======================
	// 4）REGISTER（Flags=1）优先处理
	// =======================
	remoteAddr := conn.RemoteAddr().String()

	if env.Flags == 1 && n.OnRegisterPeer != nil {
		// REGISTER：说明“我这个 ReturnPeerID，现在出现在 remoteAddr 上”
		n.OnRegisterPeer(env.ReturnPeerID, remoteAddr)
		// REGISTER 是控制层协议，不需要走 Router 流程
		return
	}

	// =======================
	// 5）可选：多跳路由学习（通过 Registry 反查来源 PeerID）
	// =======================
	var from peer.PeerID
	if n.Registry != nil {
		if id, ok := n.Registry.PeerByAddr(remoteAddr); ok {
			from = id
		}
	}

	if n.OnEnvelope != nil {
		n.OnEnvelope(from, env)
	}

	// =======================
	// 6）交给 Router 做正常的路由 / 多层解包
	// =======================
	if n.Router != nil {
		n.Router.HandleEnvelope(env)
	}
}

///////////////////////////////////////////////////////////
// 5. 主动发送：DialAndSend（一般 Demo 用）
//    通常你会用 PeerManager.SendToPeer，而不是直接用这个。
///////////////////////////////////////////////////////////

func (n *Node) DialAndSend(addr string, env *envelop.Envelope) error {
	remoteUDP, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return err
	}

	udpConn, err := net.ListenUDP("udp", nil)
	if err != nil {
		return err
	}

	tlsConf := generateTLSConfig()
	quicConf := &quic.Config{
		EnableDatagrams: true,
		MaxIdleTimeout:  3 * time.Minute,
	}

	// Dial 建立一个新的 QUIC 连接
	conn, err := quic.Dial(
		context.Background(),
		udpConn,
		remoteUDP,
		tlsConf,
		quicConf,
	)
	if err != nil {
		return err
	}
	defer conn.CloseWithError(0, "bye")

	// 打开一个单向流
	stream, err := conn.OpenUniStream()
	if err != nil {
		return err
	}

	// ========== 新版 Frame v2 打包逻辑 ==========
	// 1. Envelope → 字节
	envBytes := env.Marshal()

	// 2. 用 Frame.Build 打包（Type=Normal，长度可变）
	f := frame.NewEmptyFrame()
	if err := f.Build(frame.FrameTypeNormal, envBytes, 0); err != nil {
		return err
	}

	// 3. 写入流
	_, err = stream.Write(f.Raw)

	// 不在这里关流，由 defer conn.CloseWithError 负责
	return err
}

// 给 Node 提供一个统一的构造函数。
// 目标：
//   - 把 Name / Key / PeerManager / Registry 这些“本地网络层”的东西集中初始化
//   - Router 仍然由上层自己创建并注入（保持 Node 与 Router 的解耦）
//
// 这属于一个“简单工厂 + 依赖注入”的组合：
//   - 工厂负责把 Node 内部需要的资源就绪
//   - 上层负责把 Router（路由逻辑）插进来

// NewNode 创建一个新的 Node 实例。
//
// 参数：
//   - name：     节点名字（用于日志）
//   - key：      节点的身份密钥对（决定 PeerID）
//   - registry：地址注册表（用于 Relay / NAT Punch / 静态路由）
//   - resolver：PeerID → 地址列表 的解析函数，会交给 PeerManager 使用
//
// 返回：
//   - *Node：
//   - Name / Key 已经设置好
//   - PeerMgr 根据 resolver 创建
//   - Registry 设置为传入的 registry
//   - Router 仍然为 nil，需要上层自行设置：node.Router = yourRouter
func NewNode(
	name string,
	key *peer.KeyPair,
	registry *RelayRegistry,
	resolver func(peer.PeerID) []string,
) *Node {
	pm := NewPeerManager(resolver)
	return &Node{
		Name:     name,
		Key:      key,
		PeerMgr:  pm,
		Registry: registry,
		// Router 留给上层自己 new & 注入
	}
}
