# 旧版 Envelope / Frame 架构为什么必须升级？

## 旧架构（你最初的版本）
- Frame 固定大小 1200 字节
- Envelope 被强行塞进 Frame.Payload
- Frame.Payload = EnvelopeHeader + EnvelopePayload + Padding

## 旧架构的主要问题
1. 浪费带宽（所有帧都 1200 bytes）
2. 与 QUIC 真实网络特性不一致（QUIC 本身可变大小）
3. Frame 层和 Envelope 层严重耦合
4. 无法支持 Onion Routing（无法嵌套信封）
5. 无法支持控制帧 / 分片帧
6. 更不适合 NAT 打洞（大包影响时延）
7. 未来不可扩展（加密、签名会很痛苦）
8. Router / Node / PeerManager 逻辑被迫耦合

## 升级后的好处
- Frame = 网络层（大小、padding、类型）
- Envelope = 语义层（路由、TTL、Payload）
- NAT Punching 更成功（包小延迟低）
- 多层信封可轻松实现 Onion Routing
- 多帧类型可扩展（未来加密帧、分片帧）
- Frame 和 Envelope 完全解耦
- 代码更清晰，架构更正确
