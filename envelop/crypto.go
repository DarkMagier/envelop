package envelop

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
)

/*
===========================================================
 Envelope 加密工具（第一版）
 ----------------------------------------------------------
 目标：
   - 对 InnerPayload 做 AES-GCM 加密/解密
   - 不改 Header，不改 Router，不改 Frame 层
   - 只在业务节点（Alice/Bob）调用

 约定：
   - key: 对称密钥 []byte，长度 16/24/32（AES-128/192/256）
   - Nonce: AES-GCM 推荐 12 字节随机数，放在密文前面一起传
   - Flags: 我们约定 Flags 的 bit0 表示“InnerPayload 已加密”

   即：
     Flags & 0x01 == 1 → InnerPayload = nonce || ciphertext
===========================================================
*/

// 统一的 Flags 定义，一律用“位标志”来组合。
// 可以多个语义并存（比如既是加密又是某种控制包）。


// EncryptInner 使用给定 key 对 e.InnerPayload 进行 AES-GCM 加密。
// 加密完成后：
//   - e.InnerPayload = nonce || ciphertext
//   - e.InnerLen     = len(e.InnerPayload)
//   - e.Flags       |= FlagEncrypted
func EncryptInner(e *Envelope, key []byte) error {
	if len(e.InnerPayload) == 0 {
		// 空负载没必要加密
		return nil
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("aes.NewCipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("cipher.NewGCM: %w", err)
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("rand.Read nonce: %w", err)
	}

	// AAD（额外认证数据）：可以考虑把 Header 当作 AAD，保证头部也被认证
	// 简化起见，第一版先不加 AAD，你以后想升级可以改成：
	//   aad := e.MarshalHeaderBytes()
	//   ciphertext := aead.Seal(nil, nonce, e.InnerPayload, aad)
	// 现在先用 nil。
	ciphertext := aead.Seal(nil, nonce, e.InnerPayload, nil)

	// 把 nonce 拼在前面一块作为新的 InnerPayload
	out := make([]byte, 0, len(nonce)+len(ciphertext))
	out = append(out, nonce...)
	out = append(out, ciphertext...)

	e.InnerPayload = out
	e.InnerLen = uint16(len(out))
	e.Flags |= FlagEncrypted

	return nil
}

// DecryptInner 使用同一 key 对 e.InnerPayload 解密。
// 前提：
//   - 调用前先检查 e.Flags & FlagEncrypted != 0
//   - InnerPayload 格式 = nonce || ciphertext
func DecryptInner(e *Envelope, key []byte) error {
	// 如果没有加密标记，直接返回
	if e.Flags&FlagEncrypted == 0 {
		return nil
	}

	if len(e.InnerPayload) == 0 {
		return fmt.Errorf("encrypted flag set but payload empty")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("aes.NewCipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("cipher.NewGCM: %w", err)
	}

	nonceSize := aead.NonceSize()
	if len(e.InnerPayload) < nonceSize {
		return fmt.Errorf("payload too short for nonce")
	}

	nonce := e.InnerPayload[:nonceSize]
	ciphertext := e.InnerPayload[nonceSize:]

	plain, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return fmt.Errorf("aead.Open: %w", err)
	}

	e.InnerPayload = plain
	e.InnerLen = uint16(len(plain))
	// 解密成功后，可以去掉加密标志（看你喜好）
	e.Flags &^= FlagEncrypted

	return nil
}
