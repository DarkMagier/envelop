package router

import (
	"fmt"

	"envelop/envelop"
	"envelop/peer"
)

/*
Router 的职责（逻辑层）：

1. 识别 REGISTER 信封（Flags=1）并回调 OnRegister
2. 打印 / 检查 TTL
3. 如果信封不是发给自己：
    - 用 NextHop(Env.DestPeerID) 算下一跳 PeerID
    - 调用 Send(nextHop, env) 转发
4. 如果信封是发给自己：
    4.1 若 InnerPayload 是“下一层 Envelope”（Onion 多层信封）
        - 调用 envelop.Unmarshal 尝试解出内层 Envelope
        - 成功就递归 HandleEnvelope(innerEnv)
    4.2 否则当作业务数据，回调 OnPayload

注意：Router 不关心 IP / 端口 / QUIC，它只操作 PeerID + Envelope 这两个抽象概念。
*/

type Router struct {
	// 自己的 PeerID（用于判断 Dest 是否等于自己）
	SelfID peer.PeerID

	// 可选：路由表（R4 那一套用的）
	RouteTable *RouteTable

	// 当收到 REGISTER 信封（Flags=1）时调用
	// 参数是 ReturnPeerID（发送方 PeerID）
	OnRegister func(id peer.PeerID)

	// NextHop:
	//   输入：目标 PeerID（最终目标）
	//   输出：下一跳 PeerID（可能等于目标，也可能是中继节点）
	//
	// 这里不强制你必须用 RouteTable，简单场景可以直接：
	//   NextHop = func(dest PeerID) (PeerID, bool) { return dest, true }
	NextHop func(dest peer.PeerID) (nextHop peer.PeerID, ok bool)

	// Send:
	//   输入：下一跳 PeerID + Envelope
	//   实现通常是调用 PeerManager.SendToPeer
	Send func(nextHop peer.PeerID, env *envelop.Envelope)

	// OnPayload:
	//   当本节点是最终收件人，且 InnerPayload 不是“内层信封”时调用
	//   通常是你的业务处理 / RPC / 消息回调
	OnPayload func(env *envelop.Envelope)
}

// HandleEnvelope: 路由处理入口。
func (r *Router) HandleEnvelope(env *envelop.Envelope) {
	// ============================================
	// 0. REGISTER 信封（Flags=1）优先处理
	// ============================================
	if env.Flags == 1 {
		if r.OnRegister != nil {
			fmt.Printf("[Router %s] 收到 REGISTER 信封，来自 %s\n",
				peer.PeerIDToDomain(r.SelfID),
				peer.PeerIDToDomain(env.ReturnPeerID),
			)
			r.OnRegister(env.ReturnPeerID)
		} else {
			fmt.Println("REGISTER 信封收到，但 OnRegister 未设置")
		}
		return
	}

	// ============================================
	// 1. 打印头部信息
	// ============================================
	fmt.Printf("[Router %s] 收到 Envelope: TTL=%d Dest=%s\n",
		peer.PeerIDToDomain(r.SelfID),
		env.TTL,
		peer.PeerIDToDomain(env.DestPeerID),
	)

	// TTL 检查
	if env.TTL == 0 {
		fmt.Println("TTL=0，丢弃")
		return
	}

	// ============================================
	// 2. 如果不是给自己 → 查路由并转发
	// ============================================
	if !env.DestPeerID.Equals(r.SelfID) {
		if r.NextHop == nil || r.Send == nil {
			fmt.Println("没有 NextHop/Send 实现，无法转发。丢弃")
			return
		}
		nextHop, ok := r.NextHop(env.DestPeerID)
		if !ok {
			fmt.Println("找不到下一跳 PeerID。丢弃")
			return
		}

		env.TTL--
		fmt.Printf("→ 不是给我，转发到下一跳 %s\n", peer.PeerIDToDomain(nextHop))
		r.Send(nextHop, env)
		return
	}

	// ============================================
	// 3. 是给自己 → 处理内层信封 / 业务数据
	// ============================================
	if env.InnerLen == 0 || len(env.InnerPayload) == 0 {
		fmt.Println("→ 空信封（没有 InnerPayload）")
		return
	}

	innerBytes := env.InnerPayload

	// 3.1 尝试把 InnerPayload 当作“内层 Envelope”解析
	if innerEnv, err := envelop.Unmarshal(innerBytes); err == nil {
		fmt.Println("→ 内层是信封（Onion 一层），递归处理内层 Envelope")
		r.HandleEnvelope(innerEnv)
		return
	}

	// 3.2 否则，当作业务数据处理
	fmt.Printf("→ 收到业务数据: %q\n", string(env.InnerPayload))
	if r.OnPayload != nil {
		r.OnPayload(env)
	}
}

// 给 Router 提供一个统一的构造函数。
// 目标：
//   - 把必需字段（SelfID / RouteTable）收拢起来
//   - main.go 更简洁
//   - 以后可以很自然地扩展出别的 Router 实现（例如带 metrics / tracing 的装饰器）

// NewRouter 创建一个基础 Router。
//
// 参数：
//   - self：本节点的 PeerID（用于判断“Dest 是否是自己”）
//   - table：路由表（可以为 nil，简单直连场景可以只用 NextHop 回调）
//
// 返回：
//   - *Router：还没有设置 NextHop / Send / OnPayload，
//     需要在上层根据实际情况注入。
func NewRouter(self peer.PeerID, table *RouteTable) *Router {
	return &Router{
		SelfID:     self,
		RouteTable: table,
		// NextHop、Send、OnPayload 留给上层注入（依赖注入，降低耦合）
	}
}
