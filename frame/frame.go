////////////////////////////////////////////////////////////////////////////////
//   ★★★ 旧版本 Frame v1（固定 1200 字节）【完全保留 + 详细解释】 ★★★
//   —— 此段代码不会被编译，只作为文档保留给你参考。
//   —— 你以后想做「匿名/抗流量分析」模式时可以重新启用。
//   —— 你想要的“信封信封嵌套”也和这个 v1 完全兼容。
//
//   你最早的设计：Frame = 1200 bytes 固定大小。
//   - 这是 QUIC 最小安全 MTU（不分片）
//   - 匿名网络（I2P/Tor）通常会用“固定帧大小”抵抗流量分析
//   - 缺点：大量浪费（小消息也要填满 1200）
//   - 缺点：不灵活，无法兼容普通 P2P 场景
//
//   这一段完全用注释保留，方便你未来继续开发匿名模式。
////////////////////////////////////////////////////////////////////////////////

/*
////////////////////////////////////////////////////////////////////////////////
// ========== 旧 Frame 固定大小实现（Frame v1） ==========
//
// FrameSize = 1200 —— QUIC 安全最小包大小
// HeaderSize = 0   —— 你当时还没有 FrameType 和 Length 的定义
// PayloadSize = 1200 —— 全部放 Envelope
////////////////////////////////////////////////////////////////////////////////

// FrameSize 是你的固定帧大小（QUIC 最小安全初始包大小）。
const FrameSize = 1200

// HeaderSize 是 Frame 层用于未来扩展的空间。
// 当前 ENV 不要求这层有任何语义，所以我们保留 0。
// 但预留字段可让未来支持 checksum 或 frame ID。
const HeaderSize = 0

// PayloadSize 是 Frame 内可用于存 Envelope 的最大长度。
const PayloadSize = FrameSize - HeaderSize

// Frame 表示一个 1200 字节的固定长度数据单元。
// 注意：Frame 只负责“容器功能”，不负责任何 Envelope 语义。
//
// 序列化规则：
//   Frame.Header  （HeaderSize 字节，当前为0）
//   Frame.Payload （PayloadSize 字节）
type Frame struct {
	// Header 保留扩展字段（目前为空）。
	Header []byte

	// Payload 是存放 Envelope 的地方。
	// 长度永远固定为 PayloadSize。
	Payload []byte
}

// NewFrame 创建一个新的空 Frame。Payload 自动分配 1200 bytes。
func NewFrame() *Frame {
	return &Frame{
		Header:  make([]byte, HeaderSize),
		Payload: make([]byte, PayloadSize),
	}
}

//
// ★ 旧 Frame v1 的问题：
//   1. Envelope 不能超过 1200 bytes → 不够灵活
//   2. 1200 bytes 必须 padding → 浪费带宽
//   3. 无法区分不同类型帧（普通/匿名/控制/分片）
//   4. 无法支持大消息分片
//   5. 未来想支持 Onion Routing 必须做到“多层固定大小信封”，
//      Frame 层固定反而成为限制
//
// → 因此需要 Frame v2
//
*/

////////////////////////////////////////////////////////////////////////////////
//   ★★★ 新版本 Frame v2（可变长度 + Type flag）★★★
//
//   新设计目标：
//   1. Frame 不再固定 1200
//   2. 新增 FrameType（1 字节）
//   3. 新增 EnvelopeLen（2 字节）
//   4. Raw 字段作为最终写入 QUIC 的完整二进制数据
//
//   数据结构：
//
//   +------------+-------------------+--------------------+--------------
//   | 1 byte     | 2 bytes           | N bytes            | 可选 padding
//   +------------+-------------------+--------------------+--------------
//   | FrameType  | EnvelopeLen       | EnvelopeBytes      | (匿名模式用)
//
//   当前仅启用 FrameType=0（普通帧）
//   匿名/固定大小帧（未来） → FrameType=1，需要固定 padding
////////////////////////////////////////////////////////////////////////////////

package frame

import (
	"encoding/binary"
	"errors"
)

/*
===============================================================
 Frame v2：可变大小帧（比旧版 1200字节固定帧更先进）
 --------------------------------------------------------------
 格式：
   [1 byte Type][2 byte EnvelopeLen][EnvelopeRaw][Padding...]

 - Type 决定帧种类（未来：分片帧、控制帧、匿名帧）
 - EnvelopeLen 告诉 Router 怎么读 Envelope
 - Padding 可选，用于对抗流量分析
===============================================================
*/

/******************************************************
 * Frame v2：轻量、可变长度的封装格式
 *
 * 格式定义：
 *
 *   +---------+-------------+---------------+
 *   |  Type   |   Length    |    Payload    |
 *   | (1 B)   |   (2 B)     |   (N bytes)   |
 *   +---------+-------------+---------------+
 *
 *   - Type：帧类型（普通、分片、控制等）
 *   - Length：Payload 部分长度（uint16）
 *   - Payload：Envelope 的序列化字节
 *
 * 读写方式：
 *   - Frame.Build() → 生成 Raw 一整段字节
 *   - Frame.Decode() → 从字节切片还原 Type & Payload
 *
 * Frame v2 是“消息级长度前缀”模式，非常适合 QUIC 单向流：
 *   - 发送端写完就 CloseStream
 *   - 接收端 io.ReadAll 一次获得完整消息
 ******************************************************/

const (
	FrameHeaderSize = 3 // 1 byte Type + 2 byte Length

	FrameTypeNormal = 0x01 // 普通 Envelope（最常用）
)

// Frame 表示一个要发送/接收的 QUIC 消息 Frame
// Raw 存放最终要写入 QUIC stream 的所有数据
type Frame struct {
	Type    uint8
	Length  uint16
	Payload []byte // Envelope 的裸字节
	Raw     []byte // Type + Length + Payload
}

// ------------------------------------------------------------
// NewEmptyFrame：创建一个空 Frame（常用于 ToFrame）
// ------------------------------------------------------------
func NewEmptyFrame() *Frame {
	return &Frame{}
}

// ------------------------------------------------------------
// Build：根据类型、payload 构造一条完整 Raw 消息
// ------------------------------------------------------------
func (f *Frame) Build(t uint8, payload []byte, _ int) error {
	f.Type = t
	f.Payload = payload
	f.Length = uint16(len(payload))

	raw := make([]byte, FrameHeaderSize+len(payload))
	raw[0] = t
	binary.BigEndian.PutUint16(raw[1:3], f.Length)
	copy(raw[3:], payload)

	f.Raw = raw
	return nil
}

// ------------------------------------------------------------
// Decode：从接收到的字节中解析 Frame
//
// 返回值：
//
//	t：帧类型
//	payload：Envelope 字节
//	err：错误
//
// ------------------------------------------------------------
func Decode(data []byte) (uint8, []byte, error) {
	if len(data) < FrameHeaderSize {
		return 0, nil, errors.New("frame too short")
	}

	t := data[0]
	length := binary.BigEndian.Uint16(data[1:3])

	if len(data) < int(FrameHeaderSize+length) {
		return 0, nil, errors.New("frame length mismatch")
	}

	payload := data[3 : 3+length]
	return t, payload, nil
}
