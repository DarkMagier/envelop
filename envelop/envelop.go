package envelop

import (
	"encoding/binary"
	"envelop/frame"
	"envelop/peer"
	"fmt"
)

/*
===============================================================
 Envelope（信封）：协议语义层
 --------------------------------------------------------------
 和 Frame 完全解耦

 Frame = “怎么寄”
 Envelope = “寄什么”

 Frame 负责格式、长度、padding
 Envelope 只负责内容（TTL、DestPeerID、负载等）
===============================================================
*/

const EnvHeaderSize = 72 // 固定 header 大小
const (
	FlagEncrypted uint8 = 1 << 0 // 0000_0001：InnerPayload 已加密
	// 以后可以再加：
	// FlagAck       = 1 << 1
	// FlagOnion     = 1 << 2
	// ...

	FlagRegister uint8 = 1 << 7 // 1000_0000：控制包类型：REGISTER
	FlagRPC      uint8 = 1 << 2 // 0000 0100  ← 新增，给 RPC 用
)

// Envelope 是一层信封，可以递归嵌套。
type Envelope struct {
	Version uint8
	Flags   uint8
	TTL     uint8

	DestPeerID   peer.PeerID
	ReturnPeerID peer.PeerID

	InnerLen uint16
	Reserved [3]byte

	InnerPayload []byte
}

/*
===============================================================

	Envelope 编码逻辑（新版）
	--------------------------------------------------------------
	1. Marshal() → Envelope 结构 → 字节
	2. Unmarshal() → 字节 → Envelope 结构
	3. ToFrame() → Envelope → Frame.Raw

===============================================================
*/
func (e *Envelope) Marshal() []byte {
	buf, _ := Marshal(e)
	return buf
}

// Marshal Envelope → []byte（无 padding）
func Marshal(e *Envelope) ([]byte, error) {
	total := EnvHeaderSize + len(e.InnerPayload)
	buf := make([]byte, total)

	buf[0] = e.Version
	buf[1] = e.Flags
	buf[2] = e.TTL

	copy(buf[3:35], e.DestPeerID[:])
	copy(buf[35:67], e.ReturnPeerID[:])

	binary.BigEndian.PutUint16(buf[67:69], e.InnerLen)
	copy(buf[69:72], e.Reserved[:])

	copy(buf[72:], e.InnerPayload)

	return buf, nil
}

// []byte → Envelope
func Unmarshal(data []byte) (*Envelope, error) {
	if len(data) < EnvHeaderSize {
		return nil, fmt.Errorf("envelope too short")
	}

	e := &Envelope{}
	e.Version = data[0]
	e.Flags = data[1]
	e.TTL = data[2]

	copy(e.DestPeerID[:], data[3:35])
	copy(e.ReturnPeerID[:], data[35:67])

	e.InnerLen = binary.BigEndian.Uint16(data[67:69])
	copy(e.Reserved[:], data[69:72])

	if len(data) < EnvHeaderSize+int(e.InnerLen) {
		return nil, fmt.Errorf("inner payload truncated")
	}

	e.InnerPayload = make([]byte, e.InnerLen)
	copy(e.InnerPayload, data[72:72+e.InnerLen])
	return e, nil
}

// Envelope → Frame.Raw
func (e *Envelope) ToFrame(f *frame.Frame) error {
	b, err := Marshal(e)
	if err != nil {
		return err
	}
	return f.Build(frame.FrameTypeNormal, b, 0)
}
