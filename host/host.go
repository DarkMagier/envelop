// host/host.go
//
// Host：对外的高层封装（Facade）。
//
// 它把：
//   - RelayRegistry（地址数据库）
//   - PeerManager  （连接池、QUIC Dial）
//   - Router       （Envelope 路由）
//   - Node         （QUIC 收发、Frame/Envelope 解码）
//   - Strategy     （信封构建/解释策略）
//   - Socket       （上层统一 Send/Recv API）
// 都打包成一个“Host 对象”，对上暴露一个简洁的：
//
//   h.Send(destID, payload)
//   h.Recv() <-chan socket.IncomingMessage
//   h.Start() // ListenAndServe
//
// 这样，真正使用你框架的人，只需要配置 Host，而不用自己手动拼一堆 Node / Router / PeerManager。

package host

import (
	"fmt"
	"log"

	"envelop/envelop"
	"envelop/netquic"
	"envelop/peer"
	"envelop/router"
	"envelop/socket"
	"envelop/strategy"
)

///////////////////////////////////////////////////////////////////////////////
// 1. Host：高层封装对象（Facade）
///////////////////////////////////////////////////////////////////////////////

// Host 表示一个完整的 P2P 节点实例。
//
// 它内部组合了：
//   - RelayRegistry：地址数据库
//   - PeerManager：  连接管理 + QUIC Dial
//   - Router：       Envelope 路由逻辑
//   - Node：         处理 QUIC 联机、Frame/Envelope 收发
//   - Strategy：     构造/解释信封策略
//   - Socket：       给 App 层用的 Send/Recv 接口
//
// 对外暴露：
//   - ID()    → 返回本节点 PeerID
//   - Send()  → 发送业务数据（自动封装 Envelope + 路由）
//   - Recv()  → 收到业务数据（从 Socket 里拿）
//   - Start() → 开始监听（ListenAndServe）
type Host struct {
	id   peer.PeerID
	name string
	addr string // Listen 地址，例如 "0.0.0.0:9000"

	Registry *netquic.RelayRegistry
	PeerMgr  *netquic.PeerManager
	Router   *router.Router
	Node     *netquic.Node
	Strategy strategy.EnvelopeStrategy
	Socket   *socket.Socket
}

func (h *Host) ID() peer.PeerID { return h.id }

// Addr 返回监听地址（仅用于调试）
func (h *Host) Addr() string { return h.addr }

// Start 启动底层 Node 的监听循环。
// 一般会在外面用 go h.Start()。
func (h *Host) Start() error {
	return h.Node.ListenAndServe(h.addr)
}

// Send 直接走 Socket 的 Send。
func (h *Host) Send(dest peer.PeerID, payload []byte) error {
	return h.Socket.Send(dest, payload)
}

// Recv 返回 Socket 的消息通道。
func (h *Host) Recv() <-chan socket.IncomingMessage {
	return h.Socket.Recv()
}

///////////////////////////////////////////////////////////////////////////////
// 2. Builder：构建 Host 的参数收口（Builder 模式）
///////////////////////////////////////////////////////////////////////////////

// Builder 是一个“构建器”，用来渐进式配置 Host，避免 main.go 塞满细节。
//
// 用法示例：
//
//	h, err := host.NewBuilder().
//	    Name("Alice").
//	    Listen("0.0.0.0:9000").
//	    Build()
//
// 如果你不需要自定义 Registry / RouteTable / Strategy，可以只填 Name + Listen。
type Builder struct {
	name       string
	listenAddr string
	key        *peer.KeyPair
	registry   *netquic.RelayRegistry
	routeTable *router.RouteTable
	strategy   strategy.EnvelopeStrategy
}

// NewBuilder 创建一个空的 Builder。
func NewBuilder() *Builder {
	return &Builder{}
}

// Name 设置节点名字（用于日志）。
func (b *Builder) Name(name string) *Builder {
	b.name = name
	return b
}

// Listen 设置监听地址，例如 "0.0.0.0:9000"。
func (b *Builder) Listen(addr string) *Builder {
	b.listenAddr = addr
	return b
}

// Key 手工指定节点身份（可选）。
// 如果不指定，Build() 时会自动生成一把新 KeyPair。
func (b *Builder) Key(kp *peer.KeyPair) *Builder {
	b.key = kp
	return b
}

// Registry 手工指定全局 RelayRegistry（可选）。
// 多个 Host 共享一个 Registry 时可以传进来。
// 如果不指定，Build() 会内部创建一个新的 RelayRegistry。
func (b *Builder) Registry(rr *netquic.RelayRegistry) *Builder {
	b.registry = rr
	return b
}

// RouteTable 手工指定路由表（可选）。
// 如果不指定，可以后续再给 Router.RouteTable 赋值。
func (b *Builder) RouteTable(rt *router.RouteTable) *Builder {
	b.routeTable = rt
	return b
}

// Strategy 手工指定 EnvelopeStrategy（可选）。
// 如果不指定，Build() 会用一个默认的 SimpleStrategy。
func (b *Builder) Strategy(strat strategy.EnvelopeStrategy) *Builder {
	b.strategy = strat
	return b
}

// Build 根据当前 Builder 配置，构建一个 Host 实例。
//
// 会自动做的事情：
//   - 如果 Key 没指定：生成一把新的 KeyPair
//   - 如果 Registry 没指定：创建新的 RelayRegistry，并注册自己的静态地址
//   - 创建 PeerManager（使用 Registry.Resolver）
//   - 创建 Router，并设置：SelfID / RouteTable / NextHop / Send
//   - 创建 Node，并设置：Name / Key / Router / PeerMgr / Registry / OnRegisterPeer / OnEnvelope
//   - 如果 Strategy 没指定：用 SimpleStrategy{Key:nil, DefaultTTL:5}
//   - 创建 Socket，并接管 Router.OnPayload
func (b *Builder) Build() (*Host, error) {
	// 1）校验必要参数
	if b.listenAddr == "" {
		return nil, fmt.Errorf("Listen 地址不能为空（调用 Builder.Listen(...)）")
	}

	// 2）准备 KeyPair（如果没有外部提供）
	var kp *peer.KeyPair
	var err error
	if b.key != nil {
		kp = b.key
	} else {
		kp, err = peer.NewKeyPair()
		if err != nil {
			return nil, fmt.Errorf("NewKeyPair failed: %w", err)
		}
	}
	selfID := kp.PeerID

	// 3）准备 RelayRegistry（如果没有外部提供，就创建一个新的）
	reg := b.registry
	if reg == nil {
		reg = netquic.NewRelayRegistry()
	}
	// 确保自己的地址在 Registry 里有一条静态映射
	reg.RegisterStatic(selfID, b.listenAddr)

	// 4）创建 PeerManager，使用 Registry.Resolver 作为寻址函数
	pm := netquic.NewPeerManager(reg.Resolver)

	// 5）创建 Router，并注入 SelfID 和 RouteTable
	r := &router.Router{
		SelfID:     selfID,
		RouteTable: b.routeTable, // 可以为 nil
	}

	// NextHop：默认简化为“直连路由”（后续可结合 RouteTable 做 Kademlia）
	r.NextHop = func(dest peer.PeerID) (peer.PeerID, bool) {
		// 最简单：每个目标都视为“可直连”
		return dest, true
	}

	// Send：交给 PeerManager.SendToPeer
	r.Send = func(nextHop peer.PeerID, env *envelop.Envelope) {
		if err := pm.SendToPeer(nextHop, env); err != nil {
			log.Printf("[Router] SendToPeer error: %v", err)
		}
	}

	// OnRegister：当收到 REGISTER Envelope 时，透传给 Registry 做动态注册
	r.OnRegister = func(id peer.PeerID) {
		// 注意：真正的 addr 信息由 Node.OnRegisterPeer 负责调用 RegisterPeer，
		// 这里只是保留扩展点；当前实现中 Node.handleStream 已经提供了 OnRegisterPeer 回调。
		log.Printf("[Router] OnRegister from %s", peer.PeerIDToDomain(id))
	}

	// 6）创建 Node：绑定 Router / PeerManager / Registry
	node := &netquic.Node{
		Name:     b.name,
		Key:      kp,
		Router:   r,
		PeerMgr:  pm,
		Registry: reg,
	}

	// Node.OnRegisterPeer：当远端发来 REGISTER 信封时，把 (PeerID, addr) 注册到 Registry
	node.OnRegisterPeer = func(id peer.PeerID, addr string) {
		reg.RegisterPeer(id, addr)
	}

	// Node.OnEnvelope：可选调试回调，这里简单打印一行日志
	node.OnEnvelope = func(from peer.PeerID, env *envelop.Envelope) {
		log.Printf("[Node %s] OnEnvelope from %s → Dest=%s TTL=%d",
			b.name,
			peer.PeerIDToDomain(from),
			peer.PeerIDToDomain(env.DestPeerID),
			env.TTL,
		)
	}

	// 7）准备 Strategy：如果用户没传，就用默认 SimpleStrategy
	strat := b.strategy
	if strat == nil {
		strat = &strategy.SimpleStrategy{
			Key:        nil, // 不加密
			DefaultTTL: 5,
		}
	}

	// 8）创建 Socket：作为对上层的统一入口（Facade）
	sender := &socket.RouterEnvelopeSender{R: r}
	sock := socket.NewSocket(selfID, strat, sender, r)

	// 9）把所有东西装进 Host
	h := &Host{
		id:       selfID,
		name:     b.name,
		addr:     b.listenAddr,
		Registry: reg,
		PeerMgr:  pm,
		Router:   r,
		Node:     node,
		Strategy: strat,
		Socket:   sock,
	}

	return h, nil
}

///////////////////////////////////////////////////////////////////////////////
// 3. 便利构造：NewLocal（默认本地 Host）
///////////////////////////////////////////////////////////////////////////////

// NewLocal 是一个最简化的构造函数：
//   - 自动生成 KeyPair
//   - 自动创建 RelayRegistry
//   - 自动用 SimpleStrategy
//   - 只需要提供 Name + Listen 地址
//
// 用法：
//
//	h, err := host.NewLocal("Alice", "0.0.0.0:9000")
func NewLocal(name, listenAddr string) (*Host, error) {
	return NewBuilder().
		Name(name).
		Listen(listenAddr).
		Build()
}
