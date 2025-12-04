package router

import (
	"envelop/peer"
	"sync"
)

/*
//==========================================================
// RouteTable v2：静态直连 + Kademlia 结合版
//==========================================================
//
//设计目标：
//  1. 保留你原来那种“我明确知道 dest→via” 的静态路由：
//        LearnDirect(id)   → 认为这个 peer 可以直连
//        LearnVia(dest,via)→ 明确指定某个目标要经由哪个中继
//
//  2. 增加 KademliaTable：
//        - 输入 targetID，输出“距离最近的若干 PeerID”
//        - 当静态路由找不到 dest 时，用 Kademlia 做 fallback：
//              list := kad.FindClosest(dest, 1)
//              nextHop := list[0]
//
//  3. API 尽量不变：
//        NewRouteTable()         // 仍然是无参
//        rt.Lookup(dest)         // 用静态 + Kademlia 选下一跳
//        rt.LearnDirect(id)      // 同时喂给 Kademlia
//        rt.LearnVia(dest, via)  // 同时喂给 Kademlia
//        rt.BindSelf(selfID)     // 新增：把本节点 ID 告诉表，用于构建 Kademlia
//
//==========================================================
//*/
//
type RouteTable struct {
	mu sync.RWMutex

	selfID peer.PeerID // 本节点 ID，用于初始化 KademliaTable

	// 静态路由：dest → via
	direct map[peer.PeerID]peer.PeerID

	// Kademlia 路由视图
	kad *KademliaTable
}

// NewRouteTable 创建一张空路由表（还不知道 selfID）
func NewRouteTable() *RouteTable {
	return &RouteTable{
		direct: make(map[peer.PeerID]peer.PeerID),
	}
}

// BindSelf 绑定“本节点 ID”：
//   - 会顺便创建一张 Kademlia 表
//   - 必须在使用 Kademlia 功能前至少调用一次（一般在 main 里）
func (rt *RouteTable) BindSelf(self peer.PeerID) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	rt.selfID = self
	if rt.kad == nil {
		rt.kad = NewKademliaTable(self)
	}
}

// LearnDirect 表示“我知道这个 peer 可以直连”
// 例如：
//   - 本地有 addr 记录
//   - REGISTER 里看到 ReturnPeerID 直接接入了 Relay
func (rt *RouteTable) LearnDirect(id peer.PeerID) {
	if id.IsZero() {
		return
	}

	rt.mu.Lock()
	defer rt.mu.Unlock()

	// 静态路由：dest → via（直连时 via=dest）
	rt.direct[id] = id

	// 顺便喂给 Kademlia
	if rt.kad != nil {
		rt.kad.Update(id)
	}
}

// LearnVia 表示“我知道 dest 这个目标，需要经由 via 中继”
//
// 典型场景：多跳学习
//   - 从 from 收到了一个信封，ReturnPeerID = X
//   - 我就可以学到：想去 X，要先经过 from
func (rt *RouteTable) LearnVia(dest, via peer.PeerID) {
	if dest.IsZero() || via.IsZero() {
		return
	}

	rt.mu.Lock()
	defer rt.mu.Unlock()

	rt.direct[dest] = via
	if rt.kad != nil {
		rt.kad.Update(via)
	}
}

// Lookup 查找 dest 的下一跳 PeerID：
//  1. 先查静态 direct 映射
//  2. 如果没有，再用 Kademlia 找最近邻
func (rt *RouteTable) Lookup(dest peer.PeerID) (peer.PeerID, bool) {
	// 1. 先看有没有明确的静态路由
	rt.mu.RLock()
	if via, ok := rt.direct[dest]; ok {
		rt.mu.RUnlock()
		return via, true
	}
	kad := rt.kad
	rt.mu.RUnlock()

	// 2. 没有静态路由，尝试 Kademlia
	if kad == nil {
		return peer.PeerID{}, false
	}

	closest := kad.FindClosest(dest, 1)
	if len(closest) == 0 {
		return peer.PeerID{}, false
	}
	return closest[0], true
}

//package router
//
//import (
//	"sync"
//
//	"envelop/peer"
//)
//
//// RouteTable 是整个 Overlay Network 的“路由表”。
//// 它记录：
////
////	目标PeerID → 下一跳PeerID
////
//// 在多跳路由学习中：
//// - 下一跳（via）不是最终目的地，而是“我应该先把包发给谁”
//// - 直到层层转发最终到达 DestPeerID
////
//// 示例：
//// Alice → Relay1 → Relay2 → Bob
////
//// Relay2 收到从 Relay1 发来的包，ReturnPeerID=Alice：
////
////	学习：去 Alice → via Relay1
////
//// Relay1 收到从 Alice 发来的包，ReturnPeerID=Alice：
////
////	学习：去 Alice → via Alice（直连）
////
//// 最后形成完整路线：
////
////	Alice → via Relay1 （在 Relay2）
////	Alice → via Alice （在 Relay1）
//type RouteTable struct {
//	mu   sync.RWMutex
//	next map[peer.PeerID]peer.PeerID
//}
//
//// 创建新路由表
//func NewRouteTable() *RouteTable {
//	return &RouteTable{
//		next: make(map[peer.PeerID]peer.PeerID),
//	}
//}
//
//// ------------------------
//// 直连路由（Bootstrap）
//// ------------------------
//func (rt *RouteTable) LearnDirect(target peer.PeerID) {
//	rt.mu.Lock()
//	defer rt.mu.Unlock()
//	rt.next[target] = target
//}
//
//// ------------------------
//// 多跳路由（反向学习）
//// ------------------------
//func (rt *RouteTable) LearnRoute(target, via peer.PeerID) {
//	rt.mu.Lock()
//	defer rt.mu.Unlock()
//	rt.next[target] = via
//}
//
//// ------------------------
//// 查询路由
//// ------------------------
//func (rt *RouteTable) Lookup(target peer.PeerID) (peer.PeerID, bool) {
//	rt.mu.RLock()
//	defer rt.mu.RUnlock()
//	via, ok := rt.next[target]
//	return via, ok
//}
