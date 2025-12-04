package netquic

import (
	"log"
	"sync"

	"envelop/peer"
)

// /////////////////////////////////////////////////////////////////////////////
// RelayRegistry
//
// 这是整个网络的“地址数据库”，是所有节点寻址的核心逻辑：
//   - PeerID → []addr  （正向）
//   - addr → PeerID   （反向）
//
// 为什么必须有反向表？
//
//	因为 Node 在处理 QUIC 流时只能看到：conn.RemoteAddr().String()
//	如果没有反查，你永远不知道“这个 Envelope 是哪个 PeerID 发来的”。
//
// 多跳路由学习 (R4) 中，有一句非常重要：
//
//	“fromPeerID = Registry.PeerByAddr(remoteAddr)”
//
// 如果没有这个机制，Router 解析 Envelope 时只能看 ReturnPeerID，
// 你永远不知道这个 ReturnPeerID 是从“谁”转发来的，
// 也就无法 LearnRoute(dest, via)。
// /////////////////////////////////////////////////////////////////////////////
type RelayRegistry struct {
	mu sync.RWMutex

	// 正向：PeerID → 地址列表（IPv6 / IPv4 / 内网 等）
	addrBook map[peer.PeerID][]string

	// 反向：地址 → PeerID
	// 每当一个节点发出 REGISTER(addr)，就建立映射：
	//      addr → peerID
	revBook map[string]peer.PeerID
}

// 创建注册表
func NewRelayRegistry() *RelayRegistry {
	return &RelayRegistry{
		addrBook: make(map[peer.PeerID][]string),
		revBook:  make(map[string]peer.PeerID),
	}
}

// /////////////////////////////////////////////////////////////////////////////
// RegisterStatic
//
// 用于“系统启动前已知的节点”（Relay / Supernode / Bootstrap 节点）
// 这种节点的地址不会变，因此直接放入 addrBook + revBook。
//
// 特点：
//   - 不依赖 REGISTER Envelope
//   - 用于打通主结构：例如 Relay 和 Bob 的初始地址
//
// /////////////////////////////////////////////////////////////////////////////
func (rr *RelayRegistry) RegisterStatic(id peer.PeerID, addr string) {
	rr.mu.Lock()
	defer rr.mu.Unlock()

	rr.addrBook[id] = append(rr.addrBook[id], addr)
	rr.revBook[addr] = id

	log.Printf("[Registry] 静态注册 %s → %s", peer.PeerIDToDomain(id), addr)
}

// /////////////////////////////////////////////////////////////////////////////
// RegisterPeer
//
// 用于 REGISTER Envelope 的动态注册：
//
//	Alice → Relay 发送：Flags=1, ReturnPeerID=AliceID
//	Relay → 在 Node.handleStream 得到 remoteAddr 和 ReturnPeerID
//	RelayRegistry.RegisterPeer(AliceID, remoteAddr)
//
// 特点：
//   - PeerAddr 可能随 NAT 改变：每次 REGISTER 都更新
//   - 多次 REGISTER 可能产生多个地址：支持 NAT64 / 双栈 / 多 WiFi
//
// /////////////////////////////////////////////////////////////////////////////
func (rr *RelayRegistry) RegisterPeer(id peer.PeerID, addr string) {
	rr.mu.Lock()
	defer rr.mu.Unlock()

	// 正向表（如果地址不存在就添加）
	exists := false
	for _, a := range rr.addrBook[id] {
		if a == addr {
			exists = true
			break
		}
	}
	if !exists {
		rr.addrBook[id] = append(rr.addrBook[id], addr)
	}

	// 反向映射（始终覆盖）
	rr.revBook[addr] = id

	log.Printf("[Registry] 动态注册 %s → %s", peer.PeerIDToDomain(id), addr)
}

// /////////////////////////////////////////////////////////////////////////////
// Resolver(peerID) → []string
//
// 上层 PeerManager 调用：
//
//	addrs := relayRegistry.Resolver(peerID)
//
// 它必须返回所有可能的地址（含 IPv6 / IPv4）
// 顺序即 Dial fallback 的顺序。
// 例如：
//
//	return []string{
//	    "[2001:db8::1]:9001", // IPv6 优先
//	    "203.0.113.1:9001",   // IPv4 备用
//	}
//
// 如果你想 IPv4 先尝试，只要上层把顺序调换即可。
// /////////////////////////////////////////////////////////////////////////////
func (rr *RelayRegistry) Resolver(id peer.PeerID) []string {
	rr.mu.RLock()
	defer rr.mu.RUnlock()

	addrs := rr.addrBook[id]

	// 返回一个副本，避免外部修改内部结构
	cp := make([]string, len(addrs))
	copy(cp, addrs)
	return cp
}

// /////////////////////////////////////////////////////////////////////////////
// PeerByAddr(addr) → (PeerID, bool)
//
// ★★★ 此函数是多跳学习的关键 ★★★
//
// Node.handleStream 中：
//
//	remoteAddr := conn.RemoteAddr().String()
//	fromPeerID, ok := Registry.PeerByAddr(remoteAddr)
//
// 没这个反查，你永远无法知道“这个 Envelope 是谁转发过来的”
// /////////////////////////////////////////////////////////////////////////////
func (rr *RelayRegistry) PeerByAddr(addr string) (peer.PeerID, bool) {
	rr.mu.RLock()
	defer rr.mu.RUnlock()

	id, ok := rr.revBook[addr]
	return id, ok
}
