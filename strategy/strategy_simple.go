package strategy

import (
	"envelop/envelop"
	"envelop/peer"
	"fmt"
)

/*
==========================================================
 SimpleStrategy：一层信封 + 可选对称加密
==========================================================

场景：
- 普通 P2P 通信
- 端到端加密（Alice ↔ Bob 共用一把对称 key）

特点：
- 不做多跳 / 不做 onion，只是一层 Envelope
- Header 明文（Version/Flags/TTL/Dest/Return）
- InnerPayload：
    - 若 Key 为空：明文
    - 若 Key 不为空：EncryptInner + FlagEncrypted
*/

type SimpleStrategy struct {
	// Key:
	//   - nil 或长度为 0：不加密，纯明文
	//   - 长度为 16 / 24 / 32：使用 AES-128/192/256 进行 EncryptInner / DecryptInner
	Key []byte

	// DefaultTTL：默认 TTL；如果为 0，我们用 5。
	DefaultTTL uint8
}

// BuildOutgoing：构造一层 Envelope。
//
//	DestPeerID   = ctx.To
//	ReturnPeerID = ctx.From
//	InnerPayload = ctx.Payload（可选加密）
func (s *SimpleStrategy) BuildOutgoing(ctx SendContext) (*envelop.Envelope, error) {
	ttl := s.DefaultTTL
	if ttl == 0 {
		ttl = 5
	}

	env := &envelop.Envelope{
		Version:      1,
		Flags:        0,
		TTL:          ttl,
		DestPeerID:   ctx.To,
		ReturnPeerID: ctx.From,
		InnerPayload: ctx.Payload,
	}
	env.InnerLen = uint16(len(env.InnerPayload))

	// 如果 Key 非空，则对 InnerPayload 做 AES-GCM 加密
	if len(s.Key) > 0 {
		if err := envelop.EncryptInner(env, s.Key); err != nil {
			return nil, fmt.Errorf("EncryptInner failed: %w", err)
		}
	}

	return env, nil
}

// HandleIncoming：解释收到的一层信封。
//   - 如果有加密标记且我们有 Key → 解密
//   - 不做多层嵌套，直接认为这是业务信封。
func (s *SimpleStrategy) HandleIncoming(env *envelop.Envelope) (*envelop.Envelope, bool, error) {
	// 有加密标记 + 我们有 key → 尝试解密
	if env.Flags&envelop.FlagEncrypted != 0 && len(s.Key) > 0 {
		if err := envelop.DecryptInner(env, s.Key); err != nil {
			return nil, false, fmt.Errorf("DecryptInner failed: %w", err)
		}
	}

	// Simple 策略：一层即业务层
	return env, true, nil
}

// DumpEnvelope：debug 小工具
func DumpEnvelope(prefix string, env *envelop.Envelope) {
	fmt.Printf("%s: Dest=%s Return=%s TTL=%d Flags=%d InnerLen=%d\n",
		prefix,
		peer.PeerIDToDomain(env.DestPeerID),
		peer.PeerIDToDomain(env.ReturnPeerID),
		env.TTL,
		env.Flags,
		env.InnerLen,
	)
}

// NewSimpleStrategy 创建一个 SimpleStrategy。
//
// 参数：
//   - key：
//   - nil 或 len=0 表示不加密（纯明文传输）
//   - 长度为 16 / 24 / 32 时，可用于 AES-128/192/256（你未来在 SimpleStrategy 里可以接 AES-GCM）
//   - defaultTTL：如果为 0，则内部自动用一个“合理默认值”（例如 5）
//
// 返回：
//   - *SimpleStrategy：实现了 EnvelopeStrategy 的一层信封策略
func NewSimpleStrategy(key []byte, defaultTTL uint8) *SimpleStrategy {
	if defaultTTL == 0 {
		// 给一个默认 TTL，避免 TTL=0 被 Router 直接丢弃
		defaultTTL = 5
	}

	return &SimpleStrategy{
		Key:        key,
		DefaultTTL: defaultTTL,
	}
}
