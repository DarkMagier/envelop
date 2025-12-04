// // package 路径：envelop/rpc
package rpc

//
//import (
//	"errors"
//	"sync"
//	"time"
//
//	"envelop/envelop"
//	"envelop/peer"
//)
//
///*
//==========================================================
// RPC Client 设计说明
//==========================================================
//
//Client 负责：
//
// 1. 生成唯一的 RequestID
// 2. 把 RPCMessage 编码后塞进 Envelope 发送
// 3. 把 Pending 表里的 ID → chan *Message 对应起来
// 4. 当收到 Reply 时，根据 ID 找到对应的 channel，把结果塞进去
// 5. 提供一个 Call(...) 方法，支持超时等待
//
//注意：
//
//  - Client 不直接依赖 QUIC / Node / PeerManager，
//    只依赖一个 SendFunc：
//
//      type SendFunc func(to peer.PeerID, env *envelop.Envelope) error
//
//  - 当你在 Router.OnPayload 里收到一个 RPCReply 时，
//    需要调用 client.HandleReply(msg)，来唤醒等待中的 Call。
//==========================================================
//*/
//
//
//type Client struct {
//	selfID peer.PeerID
//	send   SendFunc
//
//	mu      sync.Mutex
//	nextID  uint64
//	pending map[uint64]chan *Message
//}
//
//func NewClient(selfID peer.PeerID, send SendFunc) *Client {
//	return &Client{
//		selfID:  selfID,
//		send:    send,
//		pending: make(map[uint64]chan *Message),
//	}
//}
//
//// Call 发送一个 RPCRequest，并等待 RPCReply 或超时
////
//// 参数：
////   - dest:    对方 PeerID（最终业务目的地）
////   - method:  方法名
////   - payload: 参数（你可以用 JSON 序列化后塞进来）
////   - timeout: 超时时间
////
//// 返回：Reply Message 或错误
//func (c *Client) Call(dest peer.PeerID, method string, payload []byte, timeout time.Duration) (*Message, error) {
//	// 1. 分配 RequestID
//	id := c.nextRequestID()
//
//	// 2. 构造 RPC Request
//	req := &Message{
//		Type:    RPCRequest,
//		ID:      id,
//		Method:  method,
//		Payload: payload,
//	}
//
//	b, err := req.Marshal()
//	if err != nil {
//		return nil, err
//	}
//
//	// 3. 构造 Envelope
//	env, err := envelop.NewBuilder().
//		Version(1).
//		Flags(0). // 可以考虑在外层加加密/Onion 等
//		TTL(10).
//		Dest(dest).
//		Return(c.selfID).
//		Payload(b).
//		Build()
//	if err != nil {
//		return nil, err
//	}
//
//	// 4. 在 pending 表里登记一个 channel
//	ch := make(chan *Message, 1)
//
//	c.mu.Lock()
//	c.pending[id] = ch
//	c.mu.Unlock()
//
//	// 5. 发送 Envelope
//	if err := c.send(dest, env); err != nil {
//		// 发送失败要清理 pending
//		c.mu.Lock()
//		delete(c.pending, id)
//		c.mu.Unlock()
//		return nil, err
//	}
//
//	// 6. 等待 Reply 或超时
//	select {
//	case reply := <-ch:
//		if reply.Error != "" {
//			return nil, errors.New(reply.Error)
//		}
//		return reply, nil
//	case <-time.After(timeout):
//		// 超时也要清理 pending
//		c.mu.Lock()
//		delete(c.pending, id)
//		c.mu.Unlock()
//		return nil, errors.New("rpc: call timeout")
//	}
//}
//
//// nextRequestID 简单自增
//func (c *Client) nextRequestID() uint64 {
//	c.mu.Lock()
//	defer c.mu.Unlock()
//	c.nextID++
//	return c.nextID
//}
//
//// HandleReply 在收到 RPCReply 时被调用，用来唤醒等待该 ID 的 Call
//func (c *Client) HandleReply(msg *Message) {
//	if msg.Type != RPCReply {
//		return
//	}
//
//	c.mu.Lock()
//	ch, ok := c.pending[msg.ID]
//	if ok {
//		delete(c.pending, msg.ID)
//	}
//	c.mu.Unlock()
//
//	if ok {
//		ch <- msg
//	}
//}
