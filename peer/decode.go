package peer

import (
	"encoding/base32"
	"errors"
	"strings"
)

/*
==========================================================
 PeerID ↔ 域名 的互相转换教程（完美对偶）
==========================================================

最重要的原则：
        **编码用什么表，解码就必须用同一个表**
否则解码永远失败。

Go 的 base32.StdEncoding = RFC4648 标准大写字母表：
    "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

因此：
    PeerIDToDomain() 必须用 StdEncoding
    DomainToPeerID() 也必须用 StdEncoding

==========================================================
*/

// 标准 Base32 编码器（RFC4648，大写字母）
// 与 PeerIDToDomain 使用完全相同
var encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// PeerID → "xxxx.env"
func PeerIDToDomain(id PeerID) string {
	enc := encoding.EncodeToString(id[:])
	return strings.ToLower(enc) + ".env" // 域名最终转小写（DNS 不区分大小写）
}

// "xxxx.env" → PeerID
func DomainToPeerID(domain string) (PeerID, error) {
	var id PeerID

	if !strings.HasSuffix(domain, ".env") {
		return id, errors.New("domain must end with .env")
	}

	// 去掉 .env
	prefix := strings.TrimSuffix(domain, ".env")
	prefix = strings.ToUpper(prefix) // RFC4648 Base32 要求使用大写

	decoded, err := encoding.DecodeString(prefix)
	if err != nil {
		return id, errors.New("base32 decode failed: " + err.Error())
	}

	if len(decoded) != PeerIDLength {
		return id, errors.New("decoded PeerID must be exactly 32 bytes")
	}

	copy(id[:], decoded)
	return id, nil
}
