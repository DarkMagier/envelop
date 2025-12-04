package rpc

import (
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

/*
==========================================================
 RPC 协议设计（教学版）
==========================================================

我们做一个极简、好理解的 RPC 层：

1. 报文结构：Message
   - Type:   是请求还是响应（Request / Response）
   - ID:     请求 ID，用于匹配响应
   - Method: 方法名，例如 "Echo"
   - Data:   参数或返回值（原始字节，外面自己决定用 JSON/CBOR 等）
   - Error:  仅 Response 用，表示错误字符串

2. 编码方式：
   - Message.Marshal()   → JSON []byte
   - rpc.Unmarshal(b)    → Message

3. 协议层和 Envelope 的关系：
   - Envelope.Flags |= FlagRPC              // 标记这是一个 RPC 报文
   - Envelope.InnerPayload = msg.Marshal()  // 业务负载就是 RPC Message

  Router 解出最终 Envelope 后：
   - 如果不是 FlagRPC：当普通数据处理
   - 如果是 FlagRPC：用 rpc.Unmarshal 再往上交给 RPC Server/Client

==========================================================
*/

// Type 表示 RPC 消息类型：请求 / 响应
type Type uint8

const (
	TypeRequest  Type = 1
	TypeResponse Type = 2
)

// Message 是一个 RPC 报文
type Message struct {
	Type   Type   `json:"t"`           // 1=Request, 2=Response
	ID     uint64 `json:"id"`          // 请求 ID
	Method string `json:"m,omitempty"` // 方法名（请求专用）
	Data   []byte `json:"d,omitempty"` // 参数或返回值
	Error  string `json:"e,omitempty"` // 错误信息（响应专用）
}

// Marshal 把 Message 编码成 JSON
func (m *Message) Marshal() ([]byte, error) {
	return json.Marshal(m)
}

// Unmarshal 从 JSON 解出 Message
func Unmarshal(b []byte) (*Message, error) {
	var m Message
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// 全局递增的请求 ID
var globalID uint64

// NewRequest 创建一个新的 RPC 请求消息
func NewRequest(method string, data []byte) *Message {
	id := atomic.AddUint64(&globalID, 1)
	return &Message{
		Type:   TypeRequest,
		ID:     id,
		Method: method,
		Data:   data,
	}
}

// NewResponse 创建一个 RPC 响应消息
func NewResponse(reqID uint64, data []byte, errStr string) *Message {
	return &Message{
		Type:  TypeResponse,
		ID:    reqID,
		Data:  data,
		Error: errStr,
	}
}

/*
==========================================================
 RPC Server：方法注册 + 调用
==========================================================
*/

// Handler 表示一个 RPC 方法：输入一段字节，返回一段字节或错误
type Handler func(reqData []byte) (respData []byte, err error)

// Server 保存 method → Handler 的映射
type Server struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

// NewServer 创建一个空的 RPC Server
func NewServer() *Server {
	return &Server{
		handlers: make(map[string]Handler),
	}
}

// Register 注册一个方法
func (s *Server) Register(method string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = h
}

// HandleMessage 处理一个 Request Message，返回 Response Message
func (s *Server) HandleMessage(msg *Message) *Message {
	if msg.Type != TypeRequest {
		return NewResponse(msg.ID, nil, "not a request")
	}

	s.mu.RLock()
	h, ok := s.handlers[msg.Method]
	s.mu.RUnlock()
	if !ok {
		return NewResponse(msg.ID, nil, "method not found: "+msg.Method)
	}

	respData, err := h(msg.Data)
	if err != nil {
		return NewResponse(msg.ID, nil, err.Error())
	}
	return NewResponse(msg.ID, respData, "")
}

/*
==========================================================
 RPC Client：发请求 + 等响应
==========================================================
*/

// Client 负责：
//  - 把发出去的 Request 按 ID 记在 pending 里
//  - 收到 Response 时，根据 ID 匹配等待者
type Client struct {
	mu      sync.Mutex
	pending map[uint64]chan *Message
}

// NewClient 创建一个 RPC Client（注意：新版不需要任何参数）
func NewClient() *Client {
	return &Client{
		pending: make(map[uint64]chan *Message),
	}
}

// OnMessage 用于接收“对方发来的 Response”并唤醒对应的等待协程
func (c *Client) OnMessage(msg *Message) {
	if msg.Type != TypeResponse {
		return
	}

	c.mu.Lock()
	ch, ok := c.pending[msg.ID]
	if ok {
		delete(c.pending, msg.ID)
	}
	c.mu.Unlock()

	if ok {
		ch <- msg
		close(ch)
	}
}

// SendFunc：Client.Call 不关心底下怎么发送，只要有人帮它把 Request 发出去就行
type SendFunc func(req *Message) error

// Call 发起一次 RPC 调用：
//   - method: 方法名
//   - data:   参数（任意序列化）
//   - send:   发送函数（由上层封装成 Envelope 并通过网络发出）
//   - timeout: 超时时间
func (c *Client) Call(
	method string,
	data []byte,
	send SendFunc,
	timeout time.Duration,
) (*Message, error) {

	// 1. 创建 Request
	req := NewRequest(method, data)

	// 2. 为这个请求准备一个等待响应的 chan
	ch := make(chan *Message, 1)

	c.mu.Lock()
	c.pending[req.ID] = ch
	c.mu.Unlock()

	// 3. 先发送请求
	if err := send(req); err != nil {
		// 发送失败要清理 pending
		c.mu.Lock()
		delete(c.pending, req.ID)
		c.mu.Unlock()
		return nil, err
	}

	// 4. 等待响应或超时
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case resp := <-ch:
		return resp, nil
	case <-timer.C:
		c.mu.Lock()
		delete(c.pending, req.ID)
		c.mu.Unlock()
		return nil, errors.New("rpc call timeout")
	}
}


//package rpc
//
//import (
//	"encoding/binary"
//	"envelop/envelop"
//	"envelop/peer"
//	"errors"
//)
//
///*
//==========================================================
// RPC 报文层设计说明
//==========================================================
//
//RPCMessage 是你“信封里的业务报文”：
//
//  QUIC → Frame → Envelope → [RPCMessage] → 你的业务代码
//
//我们用一个非常简单的二进制格式来做 RPC：
//
//  1 byte   Type        // 请求 / 响应 / 单向通知
//  8 bytes  ID          // 请求 ID，用于配对响应（big-endian）
//  2 bytes  MethodLen   // 方法名长度
//  2 bytes  PayloadLen  // 业务参数长度
//  2 bytes  ErrorLen    // 错误字符串长度（响应时使用）
//
//  后面依次跟：
//    Method   (MethodLen 字节)
//    Payload  (PayloadLen 字节)
//    Error    (ErrorLen 字节)
//
//注意：
//  - Request 一般 MethodLen>0, ErrorLen=0
//  - Response 一般 MethodLen 可为0（也可以复用），ErrorLen>0 或 =0
//  - Notify 可以 Type=RPCNotify, ID=0（不期待响应）
//
//==========================================================
//*/
//
//type RPCType uint8
//
//const (
//	RPCRequest RPCType = 1 // 有请求、有响应
//	RPCReply   RPCType = 2 // 对应某个 Request.ID
//	RPCNotify  RPCType = 3 // 仅通知，没有响应
//)
//
//// Message 是逻辑上的一条 RPC 报文
//type Message struct {
//	Type    RPCType // Request / Reply / Notify
//	ID      uint64  // 请求 ID，Reply 要和 Request 一致
//	Method  string  // 方法名（Request）/可选（Reply）
//	Payload []byte  // 业务参数（Request）或返回值（Reply）
//	Error   string  // Reply 时可能填错误信息（Error != "" 表示失败）
//}
//
//// 头部固定长度
//const headerSize = 1 + 8 + 2 + 2 + 2 // Type + ID + 3个长度字段
//
//// Marshal 把 Message 编码为 []byte，方便塞进 Envelope.InnerPayload
//func (m *Message) Marshal() ([]byte, error) {
//	methodLen := len(m.Method)
//	payloadLen := len(m.Payload)
//	errLen := len(m.Error)
//
//	if methodLen > 0xFFFF || payloadLen > 0xFFFF || errLen > 0xFFFF {
//		return nil, errors.New("rpc: field too large (>65535)")
//	}
//
//	total := headerSize + methodLen + payloadLen + errLen
//	buf := make([]byte, total)
//
//	// 1. 写头部
//	buf[0] = byte(m.Type)
//	binary.BigEndian.PutUint64(buf[1:9], m.ID)
//	binary.BigEndian.PutUint16(buf[9:11], uint16(methodLen))
//	binary.BigEndian.PutUint16(buf[11:13], uint16(payloadLen))
//	binary.BigEndian.PutUint16(buf[13:15], uint16(errLen))
//
//	// 2. 写 Method / Payload / Error
//	offset := headerSize
//	copy(buf[offset:], []byte(m.Method))
//	offset += methodLen
//
//	copy(buf[offset:], m.Payload)
//	offset += payloadLen
//
//	copy(buf[offset:], []byte(m.Error))
//
//	return buf, nil
//}
//
//// Unmarshal 解析 []byte → Message
//func Unmarshal(data []byte) (*Message, error) {
//	if len(data) < headerSize {
//		return nil, errors.New("rpc: message too short")
//	}
//
//	m := &Message{}
//	m.Type = RPCType(data[0])
//	m.ID = binary.BigEndian.Uint64(data[1:9])
//
//	methodLen := int(binary.BigEndian.Uint16(data[9:11]))
//	payloadLen := int(binary.BigEndian.Uint16(data[11:13]))
//	errLen := int(binary.BigEndian.Uint16(data[13:15]))
//
//	if headerSize+methodLen+payloadLen+errLen > len(data) {
//		return nil, errors.New("rpc: invalid length fields")
//	}
//
//	offset := headerSize
//
//	if methodLen > 0 {
//		m.Method = string(data[offset : offset+methodLen])
//		offset += methodLen
//	}
//
//	if payloadLen > 0 {
//		m.Payload = make([]byte, payloadLen)
//		copy(m.Payload, data[offset:offset+payloadLen])
//		offset += payloadLen
//	}
//
//	if errLen > 0 {
//		m.Error = string(data[offset : offset+errLen])
//	}
//
//	return m, nil
//}
//// SendFunc 是 RPC 层抽象出来的“发包函数”类型。
//type SendFunc func(to peer.PeerID, env *envelop.Envelope) error