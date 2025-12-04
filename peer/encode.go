package peer

import (
	"encoding/base32"
	"strings"
)

// base32Encoding 定义使用 RFC4648 标准 base32（不带 padding）。
//
// 设计理由：
//   - base32 可用于域名（字母 + 数字，全部小写）
//   - 不包含特殊符号，适合做 .env 域名
//   - 与 Tor、I2P、IPFS 等系统兼容性更佳
var base32Encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// EncodePeerIDToBase32 将 PeerID 编码为 base32 字符串（小写）。
//
// 例如：
//
//	peerID → "ab3kfj82kls9d8f3..."（长度 52 左右）
func EncodePeerIDToBase32(id PeerID) string {
	encoded := base32Encoding.EncodeToString(id[:])
	return strings.ToLower(encoded)
}
