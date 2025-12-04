package strategy

import (
	"envelop/envelop"
	"envelop/peer"
)

/*
==========================================================
 EnvelopeStrategy：信封策略接口（框架只管“发信封”，
 至于怎么叠信封、要不要加密、怎么回，都交给策略）
==========================================================

设计动机：

- 底层已经有：
    - QUIC + Frame（netquic）
    - Envelope（envelop）
    - Router（router）

- 但是“具体协议”不一样：
    - 有的只是普通 P2P：一层信封，明文
    - 有的需要端到端加密：一层信封，加密 InnerPayload
    - 有的需要 Onion：多层信封，一层套一层，每层不同 key
    - 有的需要 I2P/RPC：可能要复杂的回邮路径、会话等

→ 因此我们抽象出一个“策略接口”，统一入口是：
    - 我要发一条消息：From → To，Payload 是 xxx
    - 策略帮我“构造要发出去的 Envelope”
    - 对端收到 Envelope 时，用同一个策略来“解释这封信”：
        - 是业务数据？直接交给上层
        - 还是要再解一层信封？把内层信封丢回 Router
	- 底层已经有：
		- QUIC + Frame（netquic）
		- Envelope（envelop）
		- Router（router）

	- 但“上层协议”是不固定的，例如：
		- 普通 P2P：一层信封，明文
		- 端到端加密：一层信封，但 InnerPayload 加密
		- Onion：多层信封，一层套一层，每层不同 key
		- I2P / RPC：要定制回邮路径、会话等

	→ 所以抽象出 EnvelopeStrategy，让“怎么叠信封”变成一个插件。
*/

// SendContext：描述“我要发送一条消息”的意图
type SendContext struct {
	From    peer.PeerID // 谁发的
	To      peer.PeerID // 最终想发给谁（逻辑上的 To）
	Payload []byte      // 业务数据（明文）
}

// EnvelopeStrategy 是整个“信封策略”的统一接口。
//
// 用法约定：
//   - BuildOutgoing：在发送端调用（构造要发出去的最外层信封）
//   - HandleIncoming：在接收端调用（对收到的信封进行解释）
//
// HandleIncoming 的返回值：
//   - next：
//   - 如果 isBusiness == true：通常返回 env 本身（业务信封）
//   - 如果 isBusiness == false：通常返回 “内层信封”，需要 Router 再处理
//   - isBusiness：
//   - true  表示这是业务数据，可以交给上层应用
//   - false 表示这只是中间层，需要再丢回 Router 递归处理
//
// 注意：
//   - Router 层仍然负责：DestPeerID / TTL / 递归调用 HandleEnvelope
//   - Strategy 只关心“这一跳怎么解释 InnerPayload”。

// HandleIncoming：解释收到的信封。
//
//	env        : 当前节点收到的信封（已经是“给我”的）
//	返回 (next, isBusiness, error)
//
// EnvelopeStrategy：所有信封策略的统一接口。
//
// 用法约定：
//   - BuildOutgoing：发送端调用，用 ctx 构造“最外层信封”
//   - HandleIncoming：接收端调用，解释已经“给我”的信封
//
// HandleIncoming 返回：
//
//   - next：
//
//   - isBusiness == true  → 一般直接返回 env 本身（业务层信封）
//
//   - isBusiness == false → 一般返回“内层信封”，需要 Router 再处理
//
//   - isBusiness：
//
//   - true  → 这是业务数据，可以交给应用层
//
//   - false → 只是中间层，Router 需要继续递归
//
// Router 仍然负责：DestPeerID / TTL / 转发 / 再次调用 HandleEnvelope。
// Strategy 只关心“这一跳怎么解释 InnerPayload”。
type EnvelopeStrategy interface {
	// BuildOutgoing：构造要发出去的“最外层信封”。
	BuildOutgoing(ctx SendContext) (*envelop.Envelope, error)

	// HandleIncoming：解释收到的信封（已经确认 Dest 是自己的）。
	// 返回 (next, isBusiness, error)：
	//   - isBusiness=true  → next 通常是最终业务信封
	//   - isBusiness=false → next 通常是下一层信封，交给 Router 继续走一轮
	HandleIncoming(env *envelop.Envelope) (next *envelop.Envelope, isBusiness bool, err error)
}
