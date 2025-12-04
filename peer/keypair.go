package peer

import (
	"crypto/ed25519"
)

// KeyPair 表示一个节点的密钥对。
// 公钥决定 PeerID，私钥用于加密、签名、身份验证。
//
// 你未来 Envelope 层要做 onion 或签名回复，都需要 KeyPair。
//
type KeyPair struct {
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
	PeerID     PeerID
}

// NewKeyPair 生成一个新的 Ed25519 密钥对，并自动计算 PeerID。
//
// Ed25519 的优点：
//   - 签名非常快
//   - 公钥只有 32 bytes
//   - 适合 P2P 高并发节点
//
func NewKeyPair() (*KeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, err
	}

	pid := NewPeerIDFromPubKey(pub)

	return &KeyPair{
		PublicKey:  pub,
		PrivateKey: priv,
		PeerID:     pid,
	}, nil
}