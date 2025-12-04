//// package 路径：envelop/rpc
package rpc
//
//import (
//	"envelop/envelop"
//	"envelop/peer"
//	"errors"
//	"fmt"
//)
//
///*
//==========================================================
// RPC Server 设计说明
//==========================================================
//
//Server 做的事情：
//
// 1. 提供 Register(method, handler) 接口
//    - handler: func(req *Message) (*Message, error)
//
// 2. 当 Router.OnPayload 收到一个 Envelope 时：
//    - 取出 env.InnerPayload
//    - 调用 rpc.Unmarshal 解析为 RPCMessage
//    - 如果是 Request：
//         * 找到对应 Method 的 handler
//         * handler 返回一个 Reply Message
//         * 自动封装成新的 Envelope （Dest = env.ReturnPeerID）
//         * 通过 send 函数发回去
//    - 如果是 Notify：
//         * 调用 handler，但不发回应
//    - 如果是 Reply：
//         * 一般交给客户端（Client）处理，这里可以选择忽略
//
//注意：Server 本身并不知道怎么“发包”，
//      它只依赖一个函数：
//
//        SendFunc: func(to peer.PeerID, env *envelop.Envelope) error
//
//      由上层（Node / PeerManager）提供。
//==========================================================
//*/
//
//type Handler func(req *Message) (*Message, error)
//
//// Server 是一个简单的 RPC 服务端实现
//type Server struct {
//	handlers map[string]Handler
//}
//
//// NewServer 创建一个 Server
//func NewServer() *Server {
//	return &Server{
//		handlers: make(map[string]Handler),
//	}
//}
//
//// Register 注册 RPC 方法
//func (s *Server) Register(method string, h Handler) {
//	s.handlers[method] = h
//}
//
//
//
//// HandleEnvelope 由上层在 Router.OnPayload 中调用
////
//// 参数：
////   - env:    收到的最内层 Envelope（包含 RPC 报文）
////   - selfID: 当前节点的 PeerID（用于填 Reply 的 Return）
////   - send:   用于发回 Reply 的函数
////
//// 注意：
////   - 如果 payload 不是 RPC 格式，直接返回 error（上层可以当作普通数据处理）
////   - 如果是 Reply 类型，一般交给 Client 处理，这里暂时打印/忽略
//func (s *Server) HandleEnvelope(env *envelop.Envelope, selfID peer.PeerID, send SendFunc) error {
//	msg, err := Unmarshal(env.InnerPayload)
//	if err != nil {
//		return err // 说明这不是 RPC 报文，上层可以继续用其他方式处理
//	}
//
//	switch msg.Type {
//
//	case RPCRequest:
//		// 1. 找 handler
//		h, ok := s.handlers[msg.Method]
//		if !ok {
//			// 没有 handler，返回一个错误响应
//			reply := &Message{
//				Type:   RPCReply,
//				ID:     msg.ID,
//				Method: msg.Method,
//				Error:  fmt.Sprintf("rpc: no handler for method %q", msg.Method),
//			}
//			return s.sendReply(env, selfID, reply, send)
//		}
//
//		// 2. 调用 handler
//		replyMsg, err := h(msg)
//		if err != nil {
//			// handler 返回错误，也转成 Reply
//			reply := &Message{
//				Type:   RPCReply,
//				ID:     msg.ID,
//				Method: msg.Method,
//				Error:  err.Error(),
//			}
//			return s.sendReply(env, selfID, reply, send)
//		}
//
//		// 3. 正常返回
//		replyMsg.Type = RPCReply
//		replyMsg.ID = msg.ID
//		if replyMsg.Method == "" {
//			replyMsg.Method = msg.Method
//		}
//		return s.sendReply(env, selfID, replyMsg, send)
//
//	case RPCNotify:
//		h, ok := s.handlers[msg.Method]
//		if !ok {
//			return errors.New("rpc: notify with no handler: " + msg.Method)
//		}
//		// Notify 没有回复
//		_, err := h(msg)
//		return err
//
//	case RPCReply:
//		// 服务端一般不处理 Reply，这里简单打印 / 忽略
//		// 你可以选择在这里 hook 到 Client，如果双方合并。
//		fmt.Println("rpc: server got reply, usually handled by client")
//		return nil
//
//	default:
//		return errors.New("rpc: unknown message type")
//	}
//}
//
//// 内部辅助：构造 Reply Envelope 并发送
//func (s *Server) sendReply(
//	reqEnv *envelop.Envelope,
//	selfID peer.PeerID,
//	reply *Message,
//	send SendFunc,
//) error {
//	b, err := reply.Marshal()
//	if err != nil {
//		return err
//	}
//
//	// Reply 的目标就是 Request 的 ReturnPeerID
//	dest := reqEnv.ReturnPeerID
//
//	replyEnv, err := envelop.NewBuilder().
//		Version(1).
//		Flags(0). // 这里可以考虑加 FlagEncrypted 等
//		TTL(5).
//		Dest(dest).
//		Return(selfID).
//		Payload(b).
//		Build()
//	if err != nil {
//		return err
//	}
//
//	return send(dest, replyEnv)
//}
