// socket/socket.go
//
// EnvelopSocket：给“业务层 / 上层应用”使用的统一入口。
// 目标：
//   - 隐藏 Envelope / Frame / Router / PeerManager 等细节
//   - 上层只关心：Send(dest, payload) 和 Recv() <-chan IncomingMessage
//   - 后续可以在这里继续往上叠：RPC / Onion / DHT / Punch 等
//
// 注意：这是一个 Demo 级 / 教学级实现，
//       重点在于“把概念串起来”，不是生产级完整错误处理。

package socket

import (
	"fmt"

	"envelop/envelop"
	"envelop/peer"
	"envelop/router"
	"envelop/strategy"
)

///////////////////////////////////////////////////////////////////////////////
// 1. 上层看到的数据结构：IncomingMessage
///////////////////////////////////////////////////////////////////////////////

// IncomingMessage 表示“已经被 Router 认定为最终业务数据”的一条消息。
// - From：一般取自 Envelope.ReturnPeerID（最内层信封的回邮地址）
// - Payload：业务明文数据（通常是最内层 Envelope.InnerPayload）
// - Env：   对应的 Envelope（如果上层想看 TTL / Flags / ReturnPeerID 等）
type IncomingMessage struct {
	From    peer.PeerID
	Payload []byte
	Env     *envelop.Envelope
}

///////////////////////////////////////////////////////////////////////////////
// 2. EnvelopeSender 抽象：Socket 不关心底下怎么发 Envelope
///////////////////////////////////////////////////////////////////////////////

// EnvelopeSender 只负责一件事：
//
//	“给我一个最外层 Envelope，帮我把它送出去就行”
//
// 这样 Socket 就不用知道：
//   - RouteTable 怎么算下一跳
//   - PeerManager 怎么选地址、怎么复用 QUIC 连接
//   - Node / QUIC 底层怎么写 Frame / Stream
type EnvelopeSender interface {
	SendEnvelope(env *envelop.Envelope) error
}

// RouterEnvelopeSender：一个基于 Router 的 EnvelopeSender 实现。
//   - 使用 Router.NextHop 算下一跳 PeerID
//   - 使用 Router.Send    把 Envelope 交给下一跳
//
// 简单理解：
//
//	Socket →（构造最外层 Envelope）→ RouterEnvelopeSender → Router.NextHop → Router.Send → PeerManager.SendToPeer → QUIC
type RouterEnvelopeSender struct {
	R *router.Router
}

// SendEnvelope 实现 EnvelopeSender 接口。
func (s *RouterEnvelopeSender) SendEnvelope(env *envelop.Envelope) error {
	if s.R == nil {
		return fmt.Errorf("RouterEnvelopeSender: Router is nil")
	}
	if s.R.NextHop == nil || s.R.Send == nil {
		return fmt.Errorf("RouterEnvelopeSender: NextHop or Send is not set")
	}

	// 1）根据最终目标 DestPeerID 算下一跳
	nextHop, ok := s.R.NextHop(env.DestPeerID)
	if !ok {
		return fmt.Errorf("RouterEnvelopeSender: no route to %s", peer.PeerIDToDomain(env.DestPeerID))
	}

	// 2）调用 Router.Send，把信封给下一跳
	s.R.Send(nextHop, env)
	return nil
}

///////////////////////////////////////////////////////////////////////////////
// 3. EnvelopSocket 对外接口
///////////////////////////////////////////////////////////////////////////////

// EnvelopSocket 是“上层应用”真正要用的接口。
// 上层只需要：
//   - Send(dest, payload)：发一条消息
//   - Recv() <-chan IncomingMessage：异步收消息
//   - Close()：关闭通道（简单版）
type EnvelopSocket interface {
	Send(dest peer.PeerID, payload []byte) error
	Recv() <-chan IncomingMessage
	Close() error
}

///////////////////////////////////////////////////////////////////////////////
// 4. Socket 具体实现
///////////////////////////////////////////////////////////////////////////////

// Socket 是 EnvelopSocket 的一个默认实现。
// 它站在：
//   - strategy.EnvelopeStrategy（构造/解释信封）
//   - router.Router（多跳路由）
//   - EnvelopeSender（底层发送）
//
// 之上。
type Socket struct {
	// 自己的 PeerID：在 Send Context 里会用到
	selfID peer.PeerID

	// 策略：决定如何构造“最外层信封”
	//   - SimpleStrategy：一层信封 + 可选对称加密
	//   - OnionStrategy： 多层信封（目前你 Demo 里是结构为主，加密懒得写）
	strat strategy.EnvelopeStrategy

	// 底层 Envelope 发送者：可以是 Router + PeerManager 的组合
	sender EnvelopeSender

	// incoming 是一个有缓冲的通道，用于把“最终业务消息”交给上层
	incoming chan IncomingMessage

	// 关联的 Router（可选）：用于自动挂接 OnPayload 回调
	router *router.Router

	// prevOnPayload：如果 Router 原来已经有 OnPayload，我们保存起来，方便“链式调用”。
	// Demo 简化：这里只做演示，实际项目按需处理。
	prevOnPayload func(env *envelop.Envelope)

	// 是否已关闭的标志，这里简单用一个 bool 表示（并没有做原子操作）
	closed bool
}

// 确保 *Socket 实现 EnvelopSocket 接口
var _ EnvelopSocket = (*Socket)(nil)

///////////////////////////////////////////////////////////////////////////////
// 5. Socket 构造函数
///////////////////////////////////////////////////////////////////////////////

// NewSocket：底层依赖通过参数传入，方便测试 / 替换。
//   - self：    自己的 PeerID
//   - strat：   EnvelopeStrategy（如 SimpleStrategy / OnionStrategy 等）
//   - sender：  EnvelopeSender（如 RouterEnvelopeSender）
//   - r：       Router（如果不为 nil，则自动接管 Router.OnPayload）
//
// 用法示例：
//
//	s := socket.NewSocket(selfID, simpleStrategy, &RouterEnvelopeSender{R: r}, r)
func NewSocket(
	self peer.PeerID,
	strat strategy.EnvelopeStrategy,
	sender EnvelopeSender,
	r *router.Router,
) *Socket {
	s := &Socket{
		selfID:   self,
		strat:    strat,
		sender:   sender,
		incoming: make(chan IncomingMessage, 128), // 适当的缓冲，避免阻塞 Router
		router:   r,
	}

	// 如果传入了 Router，则自动接管 Router.OnPayload
	if r != nil {
		// 1）保存原来的回调（如果有）
		s.prevOnPayload = r.OnPayload
		// 2）把 Router.OnPayload 指向 Socket 的内部处理函数
		r.OnPayload = s.handleBusinessEnvelopeFromRouter
	}

	return s
}

///////////////////////////////////////////////////////////////////////////////
// 6. Send：上层只关心“我要发给谁 / 发什么”
///////////////////////////////////////////////////////////////////////////////

// Send 把“业务 payload”发给某个逻辑上的 dest PeerID。
// 内部会：
//
//	1）组装 SendContext（From / To / Payload）
//	2）交给 Strategy.BuildOutgoing 构造“最外层信封”（支持 Onion 套娃）
//	3）交给 EnvelopeSender 把信封送出去（底下再走 Router / PeerManager / QUIC）
func (s *Socket) Send(dest peer.PeerID, payload []byte) error {
	if s.closed {
		return fmt.Errorf("socket already closed")
	}
	if s.strat == nil {
		return fmt.Errorf("socket strategy is nil")
	}
	if s.sender == nil {
		return fmt.Errorf("socket sender is nil")
	}

	// 1）构造策略上下文：谁发 → 发给谁 → 发什么
	ctx := strategy.SendContext{
		From:    s.selfID,
		To:      dest,
		Payload: payload,
	}

	// 2）交给 Strategy 构造“最外层信封”
	//    - SimpleStrategy：就一层 Envelope
	//    - OnionStrategy：可能多层嵌套，返回最外层 Envelope
	outer, err := s.strat.BuildOutgoing(ctx)
	if err != nil {
		return fmt.Errorf("BuildOutgoing failed: %w", err)
	}

	// 3）交给底层 EnvelopeSender 发送
	if err := s.sender.SendEnvelope(outer); err != nil {
		return fmt.Errorf("SendEnvelope failed: %w", err)
	}

	return nil
}

///////////////////////////////////////////////////////////////////////////////
// 7. Recv：返回一个只读通道，供上层遍历消息
///////////////////////////////////////////////////////////////////////////////

// Recv 返回一个 <-chan IncomingMessage：
// 上层可以：
//
//	go func() {
//	    for msg := range sock.Recv() {
//	        fmt.Printf("from %s: %s\n", peer.PeerIDToDomain(msg.From), string(msg.Payload))
//	    }
//	}()
func (s *Socket) Recv() <-chan IncomingMessage {
	return s.incoming
}

///////////////////////////////////////////////////////////////////////////////
// 8. Router.OnPayload 挂接点：把“最终业务信封”转为 IncomingMessage
///////////////////////////////////////////////////////////////////////////////

// handleBusinessEnvelopeFromRouter 是挂在 Router.OnPayload 上的回调。
// 注意：这个回调是在“Router 确认：
//
//	1）DestPeerID == SelfID
//	2）InnerPayload 不是一个嵌套 Envelope（Onion 已拆完）
//
// 之后被调用的。
//
// 换句话说：这里收到的 env，已经是“最内层信封”。
func (s *Socket) handleBusinessEnvelopeFromRouter(env *envelop.Envelope) {
	// 0）如果 Router 原来就有 OnPayload，我们可以选择“先调用原来的，再做 Socket 的逻辑”
	//    这里我们放在前面调用，方便保持向后兼容（按需调整）。
	if s.prevOnPayload != nil {
		s.prevOnPayload(env)
	}

	// 1）如果 Socket 已经关闭，就直接丢弃
	if s.closed {
		return
	}

	finalEnv := env

	// 2）如果配置了 Strategy，且 Strategy 实现了 HandleIncoming（SimpleStrategy 有实现），
	//    我们可以在这里做一些额外处理，例如：
	//       - 解密（env.Flags & FlagEncrypted）
	//       - 未来：更智能的 Onion 剥离
	if s.strat != nil {
		if next, isBusiness, err := s.strat.HandleIncoming(env); err != nil {
			// 解密 / 处理失败，这里演示直接打印错误并退回原始 env。
			fmt.Println("[Socket] Strategy.HandleIncoming error:", err)
		} else {
			if isBusiness && next != nil {
				// SimpleStrategy 会返回 isBusiness = true，next = 已解密后的 Envelope
				finalEnv = next
			} else if !isBusiness && next != nil {
				// 理论上：如果 Strategy 想把 Onion 的下一层交给 Router 再处理，
				// 可以在这里调用 Router.HandleEnvelope(next)。
				// 不过目前你的 Onion Demo 是通过 Router 直接 Unmarshal 实现的，
				// 所以这里先留一个注释即可。
				//
				// 示例（谨慎使用）：
				//				if s.router != nil {
				//					s.router.HandleEnvelope(next)
				//					return
				//				}
				finalEnv = next
			}
		}
	}

	// 3）构造 IncomingMessage：
	//   - From：使用 ReturnPeerID（最内层信封的“回信地址”）
	//   - Payload：使用最内层 Envelope.InnerPayload（业务明文）
	msg := IncomingMessage{
		From: finalEnv.ReturnPeerID,
		// 为了安全起见，拷贝一份 payload，避免上层修改底层缓冲区。
		Payload: append([]byte(nil), finalEnv.InnerPayload...),
		Env:     finalEnv,
	}

	// 4）把消息投递到 incoming 通道。
	//    使用非阻塞写入：如果通道已满，则简单丢弃并打印日志，避免卡住 Router。
	select {
	case s.incoming <- msg:
	default:
		fmt.Println("[Socket] incoming channel is full, drop message")
	}
}

///////////////////////////////////////////////////////////////////////////////
// 9. Close：关闭 Socket
///////////////////////////////////////////////////////////////////////////////

// Close 关闭 Socket：
//   - 标记 closed = true
//   - 关闭 incoming 通道
//
// 注意：
//   - 这里没有做复杂的并发控制，只是 Demo / 教学使用。
//   - 实际项目中你可以用 sync.Once / 原子操作 / context 等方式更精细地管理生命周期。
func (s *Socket) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	close(s.incoming)
	return nil
}
