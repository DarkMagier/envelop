// strategy/strategy_onion.go
package strategy

import (
	"envelop/envelop"
	"envelop/peer"
)

type OnionStrategy struct{}

func NewOnionStrategy() *OnionStrategy { return &OnionStrategy{} }

// path = [Relay1, Relay2, ..., FinalDest]
func (s *OnionStrategy) BuildEnvelope(
	dest peer.PeerID,
	from peer.PeerID,
	payload []byte,
	path []peer.PeerID,
) (*envelop.Envelope, error) {

	// 最内层信封 → 目标 Bob
	inner, _ := envelop.NewBuilder().
		Version(1).
		Flags(0).
		TTL(5).
		Dest(dest).
		Return(from).
		Payload(payload).
		Build()

	// 反向包成：外层→内层
	for i := len(path) - 1; i >= 0; i-- {
		relay := path[i]
		innerBytes := inner.Marshal()

		inner, _ = envelop.NewBuilder().
			Version(1).
			Flags(0).
			TTL(5).
			Dest(relay).
			Return(from).
			Payload(innerBytes).
			Build()
	}

	return inner, nil
}

//package strategy
//
//import (
//	"envelop/envelop"
//	"envelop/peer"
//	"fmt"
//)
//
///*
//==========================================================
// OnionLayer & BuildOnion：多层信封套娃的构造工具
//==========================================================
//
//假设路径：Alice → Relay1 → Relay2 → Bob
//
//发送端（Alice）准备：
//
//    layers := []OnionLayer{
//        {Dest: relay1ID, Key: keyAR1}, // 最外层
//        {Dest: relay2ID, Key: keyR1R2},
//        {Dest: bobID,    Key: keyR2B}, // 最内层
//    }
//
//finalPayload := []byte("Hello Bob")
//outerEnv, _ := BuildOnion(layers, finalPayload)
//
//然后只要把 outerEnv 交给你的 QUIC+Frame+Router 发出去。
//*/
//
//// OnionLayer 表示 Onion 路径中的一层。
//type OnionLayer struct {
//	Dest peer.PeerID // 这一层的目标节点（本层 Envelope.DestPeerID）
//	Key  []byte      // 这一层用于加密 InnerPayload 的对称密钥
//}
//
//// ErrInvalidOnion：空层或其他无效参数时返回
//var ErrInvalidOnion = fmt.Errorf("invalid onion layers")
//
//// BuildOnion：根据给定的 OnionLayer 切片，构造多层信封。
////
////	layers: 建议按“第一跳 → 最后一跳”的顺序：
////	            layers[0] = 第一个中继（最外层）
////	            layers[n-1] = 最终目标（最内层）
////
////	finalPayload: 最内层要给最终目标的明文业务数据
////
//// 返回：最外层 Envelope（Dest = layers[0].Dest）
//func BuildOnion(layers []OnionLayer, finalPayload []byte) (*envelop.Envelope, error) {
//	if len(layers) == 0 {
//		return nil, ErrInvalidOnion
//	}
//
//	// 1. 先构造最内层信封：Dest = 最后一跳（最终收信人）
//	last := len(layers) - 1
//	innerEnv := &envelop.Envelope{
//		Version:      1,
//		Flags:        0,
//		TTL:          16, // 设大一点，防止多跳提前 TTL=0
//		DestPeerID:   layers[last].Dest,
//		ReturnPeerID: peer.PeerID{}, // 暂不设计回邮路径
//		InnerPayload: finalPayload,
//	}
//	innerEnv.InnerLen = uint16(len(innerEnv.InnerPayload))
//
//	current := innerEnv
//
//	// 2. 从倒数第二层开始，逐层“往外包”
//	for i := last - 1; i >= 0; i-- {
//		layer := layers[i]
//
//		// 2.1 把当前信封序列化成字节
//		plain, err := envelop.Marshal(current)
//		if err != nil {
//			return nil, fmt.Errorf("Marshal onion layer %d failed: %w", i, err)
//		}
//
//		// 2.2 构造上一层 Envelope，Dest = 这一层的中继节点
//		nextEnv := &envelop.Envelope{
//			Version:      1,
//			Flags:        0, // EncryptInner 会设置 FlagEncrypted
//			TTL:          16,
//			DestPeerID:   layer.Dest,
//			ReturnPeerID: peer.PeerID{}, // 中继通常不用回邮
//			InnerPayload: plain,
//		}
//		nextEnv.InnerLen = uint16(len(plain))
//
//		// 2.3 用这一层的 key 对 InnerPayload 加密
//		if len(layer.Key) > 0 {
//			if err := envelop.EncryptInner(nextEnv, layer.Key); err != nil {
//				return nil, fmt.Errorf("EncryptInner in onion layer %d failed: %w", i, err)
//			}
//		}
//
//		// 2.4 current 更新为新的外层
//		current = nextEnv
//	}
//
//	// 3. 最终 current 就是最外层信封
//	return current, nil
//}
//
///*
//==========================================================
// OnionHopStrategy：每一跳如何“解一层皮”
//==========================================================
//
//在每个中继 / 终点节点上，我们配置一个 OnionHopStrategy：
//
//    hop := &OnionHopStrategy{
//        SelfID: myPeerID,
//        Key:    myOnionKey,
//    }
//
//当 Router 判断“这个信封是给我”的时候，调用：
//
//    next, isBiz, _ := hop.HandleIncoming(env)
//
//- 若 isBiz == false：
//    → next 是内层 Envelope，交给 Router.HandleEnvelope(next)
//
//- 若 isBiz == true：
//    → next 是业务信封（最内层），交给应用层
//*/
//
//type OnionHopStrategy struct {
//	SelfID peer.PeerID // 当前节点的 PeerID（可选，用来做严格校验）
//	Key    []byte      // 当前节点用于解这一层 Onion 的对称 key
//}
//func NewOnionStrategy() *OnionHopStrategy { return &OnionHopStrategy{} }
//
//// BuildOutgoing BuildOutgoing：中继一般不会自己构造完整 Onion，
//// 所以这里直接提示使用 BuildOnion。
//func (o *OnionHopStrategy) BuildOutgoing(ctx SendContext) (*envelop.Envelope, error) {
//	return nil, fmt.Errorf("OnionHopStrategy does not support BuildOutgoing; use BuildOnion on sender side")
//}
//
//// HandleIncoming HandleIncoming：解一层 Onion。
////
//// 步骤：
////  1. （可选）检查 DestPeerID 是否 = SelfID
////  2. 若 Flags 有加密标记，则用 Key 解密 InnerPayload
////  3. 尝试把 InnerPayload 解析成下一层 Envelope：
////     - 成功 → 返回 innerEnv, false（还有下一层，交给 Router）
////     - 失败 → 返回 env, true   （说明这已经是最内层业务）
//func (o *OnionHopStrategy) HandleIncoming(env *envelop.Envelope) (*envelop.Envelope, bool, error) {
//	// 1. 严格一点可以校验一下目的是否是自己
//	if !env.DestPeerID.IsZero() && !env.DestPeerID.Equals(o.SelfID) {
//		// 这里不直接报错，只做温和设计。
//		// 真要严控可以改成 return nil, false, fmt.Errorf("onion hop dest mismatch")
//	}
//
//	// 2. 如有加密标记，先解密
//	if env.Flags&envelop.FlagEncrypted != 0 {
//		if len(o.Key) == 0 {
//			return nil, false, fmt.Errorf("encrypted onion layer but no key configured")
//		}
//		if err := envelop.DecryptInner(env, o.Key); err != nil {
//			return nil, false, fmt.Errorf("DecryptInner in onion hop failed: %w", err)
//		}
//	}
//
//	// 3. 尝试把 InnerPayload 当作下一层 Envelope 解析
//	innerEnv, err := envelop.Unmarshal(env.InnerPayload)
//	if err != nil {
//		// 解析失败：说明 InnerPayload 不是一个 Envelope，
//		// 也就是说这已经是业务明文。
//		return env, true, nil
//	}
//
//	// 解析成功：还有下一层 Onion，要交给 Router 继续递归。
//	return innerEnv, false, nil
//}
