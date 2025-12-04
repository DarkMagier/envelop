# Envelop


<p>
  🌏 
  <b>中文</b> · <a href="README.md">English</a>
</p>


> 一个基于 QUIC 的「信封式」P2P 协议与框架。  
> 真正的报文是一张纸，这张纸被塞进一层层信封里，在节点之间传递。



---

## 🧾 Envelop 是什么？

Envelop 的核心理念非常简单：

> **业务数据是一张纸（Paper），传输时永远要装进一个信封（Envelope）。**

- 纸：你真正关心的东西
    - 一条聊天消息
    - 一个 RPC 请求
    - 一段 HTTP 报文
    - ……任何业务数据

- 信封：负责「怎么寄送」
    - 外面写着：收件人（DestPeerID）、寄件人（ReturnPeerID）、TTL、Flags
    - 里面装着：下一层信封，或者最终的那张纸

信封可以继续塞进更外层的信封，形成 Onion 风格的「套娃」：

- 最里层：寄给 C 的信封，装着纸
- 把「寄给 C 的信封」当纸，塞进「寄给 B 的信封」
- 再把「寄给 B 的信封」塞进「寄给 A 的信封」
- 你只需要寄给 A，后面 A→B→C 自己按规则拆信、转发

Envelop 做的，就是把这一整套「纸 + 信封 + 套娃 + 转发」变成一个可复用的框架：

- 底层：QUIC + Frame v2
- 中层：Envelope v2 + Router + Strategy
- 上层：Socket + Host，给应用提供简单的 `Send/Recv` API

---

## 🧱 当前已经实现了什么？

### 1. 传输层：QUIC + Frame v2（`netquic` + `frame`）

- 使用 QUIC 作为传输协议（多路复用、拥塞控制、适合复杂网络环境）
- 每个 QUIC 流承载一个 Frame：
    - `Type | Length | Payload` 的可变长帧格式
    - 方便做边界控制和调试

### 2. 信封层：Envelope v2（`envelop`）

- 定义了标准的 Envelope 结构：

  ```text
  +-------------------------------+
  | Version | Flags | TTL        |
  +-------------------------------+
  | DestPeerID    | ReturnPeerID |
  +-------------------------------+
  | InnerLength   | InnerPayload |
  +-------------------------------+
  ```

- `InnerPayload` 可以是：
    - **下一层 Envelope**
        - 由 Strategy 决定如何封装/解封
        - 用来实现 Onion 套娃、多跳路由、混合网络等高级玩法
    - **业务数据**
        - 最内层的明文 / 密文负载
        - 由上层应用自己解释（RPC、聊天、HTTP、文件块等）

### 3. 路由层：Router（`router`）

- 基于 `DestPeerID / TTL` 做多跳路由：
    - `NextHop(dest) → nextPeerID`：决定下一跳是谁（可以接 DHT）
    - `Send(nextHop, env)`：真正把 Envelope 发出去（通常由 PeerManager 实现）
- 支持 REGISTER 控制包，用于：
    - 注册 PeerID ↔ 地址
    - 做 NAT Punch / Relay 等

Router 对 Envelope 的态度很简单：

> 只看「信封外面」的东西（Dest/Return/TTL/Flags），  
> 不关心里面那张纸具体写了什么。

### 4. 策略层：Strategy（`strategy`）

通过 `EnvelopeStrategy` 接口，把「怎么封/怎么拆每一层信封」抽象出来：

- `BuildOutgoing(ctx)`：从 `(From, To, Payload)` 构造最外层 Envelope
- `HandleIncoming(env)`：收到信封时，判断：
    - 这是业务数据 → 交给上层
    - 还是 Onion 的下一层 → 交回 Router 继续处理

当前实现：

- `SimpleStrategy`：
    - 单层信封
    - 支持配置默认 TTL
    - 目前 Demo 版不做加密，重点在结构与路由

未来可以在此基础上扩展出：

- 每层独立加密的 Onion 策略
- 不同混淆/匿名策略（I2P/Tor 风格）等

### 5. Socket 抽象：EnvelopSocket（`socket`）

为应用提供一个更「像 Socket」的 API：

- `Send(dest peer.PeerID, payload []byte) error`
- `Recv() <-chan IncomingMessage`

内部自动完成：

1. 调用 Strategy 构造最外层 Envelope（可套娃）
2. 交给 Router 做多跳路由
3. 收到最终业务信封后：
    - 由 Strategy 决定是否还有内层
    - 提取出业务 Payload，投递到 `IncomingMessage` 通道

> 应用层只面对「纸」，不必自己操作「信封」。

### 6. Host 封装（`host`）

`Host` = Registry + PeerManager + Router + Node + Strategy + Socket 的组合体。

对外暴露的是一个尽量简单的接口：

- `host.NewLocal(name, listenAddr)`：快速创建一个本地节点
- `Host.Start()`：启动 QUIC 监听
- `Host.ID()`：获取当前节点 PeerID
- `Host.Send(peerID, payload)` / `Host.Recv()`：应用层入口

Host 内部自动完成：

- 生成/管理 KeyPair
- Address Registry（RelayRegistry）
- PeerManager 初始化与 Resolver 注入
- Router 初始化（SelfID / NextHop / Send）
- Node 初始化（监听、处理 Frame、交给 Router）
- Strategy + Socket 的接线

---

## 📝 设计理念：纸和信封（详细版）

在 Envelop 中，一切都围绕一个类比展开：

> **Paper（纸） + Envelope（信封）**

### 纸（Paper）：真正的报文

- 真正的业务内容：
    - 比如「给 Bob 发一句：你好」
    - 比如「对某个 API 的请求」
- 在代码里，它通常是：
    - 最内层 Envelope 的 `InnerPayload`

### 信封（Envelope）：负责寄送

- 包含：
    - 收件人：`DestPeerID`
    - 寄件人：`ReturnPeerID`
    - 寄送规则：TTL / Flags
    - 内容长度：InnerLength
    - 内容：InnerPayload（可以是纸，也可以是另一个信封）

> 你可以把 Envelope 看成一封「带路由信息的密封信」。

### 转发模式：只套一层信封

最简单的使用方式是「只套一层信封」：

1. 把纸塞进 Envelope
2. 写上 Dest = B，Return = A
3. 发出去

中间经过的节点：

- 如果自己不是 B：
    - 看 Dest 是 B → 按 NextHop 规则转发
- 如果自己是 B：
    - 拆信封 → 拿出纸 → 交给上层（比如聊天应用）

这就是 `SimpleStrategy` + Router 当前做的事情。

### 套娃模式：纸 → C → B → A

多层信封形成一个 Onion：

1. 最里层：`Envelope C`（Dest = C）
2. 中间层：`Envelope B`（InnerPayload = marshal(Envelope C), Dest = B）
3. 最外层：`Envelope A`（InnerPayload = marshal(Envelope B), Dest = A）

寄送流程：

- 你只寄给 A
- A 拆自己的信封，发现里面是一封寄给 B 的信 → 转发给 B
- B 拆自己的信封，发现里面是一封寄给 C 的信 → 转发给 C
- C 拆自己的信封，终于看到最内层的那张纸

特点：

- A、B 都只能拆「写着自己名字」那一层信封
- 看到的只是：
    - 下一个收件人是谁
    - 自己这层的寄送信息
- 如果内层做了加密，A、B 看不到纸的内容

在 Envelop 里：

- 套娃逻辑由 Strategy 决定（怎么封、封几层、每层 Dest/Return 填什么）
- Router 只负责根据 Dest/TTL 把信送到该去的节点
- 节点拿到信封后，可以选择：
    - 拆开看到是纸 → 交给应用
    - 拆开还是信封 → 再交给 Router → 继续寄送
    - 重新封装成新的信封 → 再寄出去

> **具体怎么寄送，由收件人决定**  
> 协议只负责保证「信封能按 DestPeerID 抵达对的节点」。

---

## 🚀 快速开始（Quick Start）

### 环境要求

- Go 1.21+（go.mod 中声明为 1.25，建议使用较新的 Go 版本）

### 运行仓库内最小 Demo

```bash
git clone https://github.com/yourname/envelop.git
cd envelop
go run .
```

当前根目录的 `main.go` 会做一件事情：

- 创建一个 Host
- 在本地监听（例如 0.0.0.0:9000）
- 给自己发一条消息
- 从 `Recv()` 中打印收到的内容

### 在你自己的项目中使用

```go
package main

import (
	"fmt"
	"log"
	"time"

	"envelop/host"
	"envelop/peer"
)

func main() {
	// 1）创建一个本地 Host，自动初始化底层组件：
	//    - KeyPair（身份）
	//    - RelayRegistry（地址表）
	//    - PeerManager（连接池）
	//    - Router（路由）
	//    - Node（QUIC）
	//    - Strategy + Socket
	h, err := host.NewLocal("DemoNode", "0.0.0.0:9000")
	if err != nil {
		log.Fatal("NewLocal error:", err)
	}

	fmt.Println("My PeerID =", peer.PeerIDToDomain(h.ID()))
	fmt.Println("Listening on", h.Addr())

	// 2）启动底层 QUIC 监听
	go func() {
		if err := h.Start(); err != nil {
			log.Fatal("Host.Start error:", err)
		}
	}()

	// 3）应用层接收循环
	go func() {
		for msg := range h.Recv() {
			fmt.Printf("[App] from %s: %s\n",
				peer.PeerIDToDomain(msg.From),
				string(msg.Payload),
			)
		}
	}()

	// 4）Demo：给自己发一条消息（自发自收）
	time.Sleep(time.Second)
	if err := h.Send(h.ID(), []byte("hello from Host API")); err != nil {
		log.Println("Send error:", err)
	}

	select {}
}
```

> ⚠️ 当前版本以「实验/学习」用途为主  
> 协议仍在演进中，请不要直接用于生产环境或安全敏感场景。

---

## 📚 核心模块概览

### `envelop/` — Envelope v2

- 定义 Envelope 结构与编解码逻辑
- 只关心「信封长什么样」，不关心业务内容

### `frame/` — Frame v2

- 定义 `Type + Length + Payload` 的帧格式
- 用于在 QUIC stream 上承载一条条消息

### `peer/` — PeerID & KeyPair

- 管理节点身份：
    - `KeyPair`：公私钥对
    - `PeerID`：公钥派生的标识符
- 提供 `PeerIDToDomain` 等辅助函数

### `netquic/` — QUIC 节点与连接管理

- `Node`：
    - 绑定 Router
    - 负责：
        - 启动监听（ListenAndServe）
        - 接收 Frame → 解析成 Envelope → 交给 Router
        - 将 Router 发出的 Envelope 写入 QUIC stream
- `PeerManager`：
    - 管理与各 Peer 的 QUIC 连接
    - 对上暴露 `SendToPeer(peerID, env)` 接口
- `RelayRegistry`：
    - 管理 PeerID ↔ 地址映射
    - 支持静态和动态注册，方便 NAT Punch / Relay

### `router/` — Router & RouteTable

- 根据 DestPeerID / TTL 决定 Envelope 的去向
- 通过函数注入方式获得：
    - `NextHop`：如何找到下一跳
    - `Send`：如何发包给下一跳
- 支持 REGISTER / OnPayload / OnRegister 等回调

### `strategy/` — EnvelopeStrategy

- 约定一层信封「怎么封」「怎么拆」：
    - `BuildOutgoing`
    - `HandleIncoming`
- 当前提供：
    - `SimpleStrategy`：单层信封 Demo 实现

### `socket/` — EnvelopSocket

- 对上暴露：
    - `Send(dest, payload)`
    - `Recv() <-chan IncomingMessage`
- 内部桥接 Strategy + Router

### `host/` — Host & Builder

- 提供高层封装：
    - `NewLocal(name, listenAddr)`：一行创建节点
    - `Host.Start()`：启动
    - `Host.Send / Host.Recv`：应用层接口
- 将 Node / Router / PeerManager / Registry / Strategy / Socket 组合在一起

---

## 📁 仓库结构

```text
envelop/
  go.mod
  main.go              # 最小 Demo：使用 Host.NewLocal + 自发自收

  envelop/             # Envelope v2 定义与编解码
  frame/               # Frame v2
  peer/                # KeyPair / PeerID
  netquic/             # QUIC Node / PeerManager / RelayRegistry
  router/              # Router / RouteTable / DHT 基础
  strategy/            # EnvelopeStrategy 接口 + SimpleStrategy
  socket/              # EnvelopSocket：Send/Recv Facade
  host/                # Host + Builder：高层封装


```

---

## 🛣️ 未来技术规划

> 以下为技术方向上的想法，仅作为参考。

- **协议层**
    - 完成 Frame v2 / Envelope v2 的文档化（`docs/protocol.md`）
    - 完善 Flags 语义：REGISTER / Encrypted / Control 等

- **安全与匿名**
    - 实现真正的 Onion 多层加密策略（每跳独立密钥）
    - 为 Envelope 增加 MAC / 签名，防篡改与重放攻击

- **路由与 DHT**
    - 基于现有 RouteTable / FIND_NODE 完善 Kademlia 路由
    - 支持多种 NextHop 策略（直连 / 中继 / 混合）

- **HTTP / 代理集成**
    - 在 Host 之上实现本地客户端代理（HTTP/HTTPS → Envelop）
    - 提供与 Nginx / Caddy 的简单集成示例

---

## 🪪 License

本项目使用 **AGPLv3** 协议开源。  
详见仓库中的 [`LICENSE`](LICENSE) 文件。
