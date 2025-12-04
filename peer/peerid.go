package peer

import (
	_ "crypto/ed25519"
	_ "crypto/rand"
	"crypto/sha256"
	_ "encoding/base32"
	"errors"
	_ "fmt"
)

// PeerIDLength 表示 PeerID 的固定长度。
// 约定 PeerID = SHA256(public_key)，取前 32 字节。
const PeerIDLength = 32

// PeerID 表示一个节点的身份标识。
// Envelope 层、路由层都会用它来判断“这个信封给谁的”。
type PeerID [PeerIDLength]byte

// NewPeerIDFromPubKey 根据公钥生成 PeerID。
func NewPeerIDFromPubKey(pubkey []byte) PeerID {
	sum := sha256.Sum256(pubkey)
	var id PeerID
	copy(id[:], sum[:PeerIDLength])
	return id
}

// IsZero 判断 PeerID 是否为空（全零）。
func (p PeerID) IsZero() bool {
	for _, b := range p {
		if b != 0 {
			return false
		}
	}
	return true
}

// Equals 判断两个 PeerID 是否相等。
func (p PeerID) Equals(other PeerID) bool {
	return p == other
}

// ValidatePeerID 校验输入是否是合法 PeerID 长度。
func ValidatePeerID(b []byte) error {
	if len(b) != PeerIDLength {
		return errors.New("peer_id 长度必须为 32 字节")
	}
	return nil
}

// =============================
//  KeyPair & 生成节点身份
// =============================

//// KeyPair 表示一个节点的密钥对 + PeerID。
//type KeyPair struct {
//	PrivateKey ed25519.PrivateKey
//	PublicKey  ed25519.PublicKey
//	PeerID     PeerID
//}

//// NewKeyPair 生成一个新的节点密钥对和 PeerID。
//func NewKeyPair() (*KeyPair, error) {
//	pub, priv, err := ed25519.GenerateKey(rand.Reader)
//	if err != nil {
//		return nil, fmt.Errorf("生成 ed25519 密钥失败: %w", err)
//	}
//	id := NewPeerIDFromPubKey(pub)
//
//	return &KeyPair{
//		PrivateKey: priv,
//		PublicKey:  pub,
//		PeerID:     id,
//	}, nil
//}

//// PeerIDToDomain 把 PeerID 转成一个“可读域名风格”的字符串。
//// 这里用 base32（无 padding），你也可以换成 hex 等。
//func PeerIDToDomain(id PeerID) string {
//	enc := base32.StdEncoding.WithPadding(base32.NoPadding)
//	s := enc.EncodeToString(id[:])
//	// 简单加个前缀，方便识别
//	return "peer-" + s
//}
