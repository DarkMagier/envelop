# Envelop


<p>
  🌏 
  <a href="README_zh.md">中文</a> · <b>English</b>
</p>


> A QUIC-based, “envelope-style” P2P protocol and framework.  
> The real message is just a sheet of paper, sealed inside one or more envelopes and passed between nodes.

---

## 🧾 What is Envelop?

The core idea of Envelop is very simple:

> **Your business data is a sheet of paper (Paper), it is *always* transported inside an envelope (Envelope).**

- **Paper**: what you actually care about
  - a chat message
  - an RPC request
  - an HTTP payload
  - …any kind of application data

- **Envelope**: responsible for “how to deliver it”
  - On the outside: recipient (DestPeerID), sender (ReturnPeerID), TTL, Flags
  - Inside: the next envelope, or the final sheet of paper

An envelope can be put into another envelope, forming an Onion-style “matryoshka”:

- Innermost: an envelope addressed to C, containing the paper
- Take the “to C” envelope as the paper, put it into an envelope addressed to B
- Take the “to B” envelope as the paper, put it into an envelope addressed to A
- You only send to A; then A → B → C each open “their own layer” and forward according to the rules

Envelop turns this whole model — **Paper + Envelope + nesting + forwarding** — into a reusable framework:

- Transport layer: QUIC + Frame v2  
- Middle layer: Envelope v2 + Router + Strategy  
- Upper layer: Socket + Host, exposing a simple `Send/Recv` API to applications

---

## 🧱 What is implemented today?

### 1. Transport: QUIC + Frame v2 (`netquic` + `frame`)

- QUIC as the transport protocol:
  - multiplexed streams
  - congestion control
  - more robust in complex networks
- Each QUIC stream carries exactly one Frame:
  - variable-length `Type | Length | Payload` format
  - easier boundary control and debugging

### 2. Envelope layer: Envelope v2 (`envelop`)

- Standard Envelope structure:

  ```text
  +-------------------------------+
  | Version | Flags | TTL        |
  +-------------------------------+
  | DestPeerID    | ReturnPeerID |
  +-------------------------------+
  | InnerLength   | InnerPayload |
  +-------------------------------+
  ```

- `InnerPayload` can be:
  - **The next Envelope**
    - how to wrap/unwrap is decided by a Strategy
    - used to implement Onion nesting, multi-hop routing, mixnet-style behavior, etc.
  - **Application data**
    - the innermost plaintext / ciphertext
    - interpreted by the application (RPC, chat, HTTP, file chunks, …)

### 3. Routing layer: Router (`router`)

- Multi-hop routing based on `DestPeerID / TTL`:
  - `NextHop(dest) → nextPeerID`: decide the next hop (can be DHT-backed)
  - `Send(nextHop, env)`: actually send the Envelope (usually via PeerManager)
- Supports REGISTER control envelopes, used for:
  - registering PeerID ↔ address mappings
  - NAT punch / relay scenarios

The Router’s attitude towards Envelope is simple:

> It only looks at the *outside* of the envelope (Dest/Return/TTL/Flags),  
> and doesn’t care what the paper inside says.

### 4. Strategy layer: Strategy (`strategy`)

`EnvelopeStrategy` abstracts “how to build / how to interpret each layer of envelope”:

- `BuildOutgoing(ctx)`: from `(From, To, Payload)` build the outermost Envelope
- `HandleIncoming(env)`: when receiving an Envelope, decide:
  - this is final application data → deliver to upper layer
  - this is the next Onion layer → hand back to Router for further processing

Currently implemented:

- `SimpleStrategy`:
  - single-layer envelope
  - configurable default TTL
  - demo version does not do encryption yet, focuses on structure and routing

On top of this you can later build:

- truly per-hop encrypted Onion strategies
- different obfuscation / anonymity strategies (Tor / I2P-style, etc.)

### 5. Socket abstraction: EnvelopSocket (`socket`)

Provides an API that *feels* like a simple message socket for applications:

- `Send(dest peer.PeerID, payload []byte) error`
- `Recv() <-chan IncomingMessage`

Under the hood it automatically:

1. Calls the Strategy to build the outermost Envelope (possibly nested)
2. Hands it to the Router for multi-hop delivery
3. On the receiving side:
   - uses Strategy to decide whether there is another inner layer
   - extracts final application payload and pushes it into the `IncomingMessage` channel

> The application layer deals only with “paper”, and never has to manually manipulate envelopes.

### 6. Host: high-level node wrapper (`host`)

`Host` is a composition of:

> Registry + PeerManager + Router + Node + Strategy + Socket

It exposes a minimal, friendly interface:

- `host.NewLocal(name, listenAddr)`: quickly create a local node
- `Host.Start()`: start the QUIC listener
- `Host.ID()`: get the node’s PeerID
- `Host.Send(peerID, payload)` / `Host.Recv()`: application-level entry points

Internally, Host automatically:

- generates/manages the KeyPair
- sets up the Address Registry (RelayRegistry)
- initializes PeerManager and injects resolver
- configures Router (SelfID / NextHop / Send)
- sets up Node (listening, handling Frames, calling Router)
- wires up Strategy + Socket

---

## 📝 Design idea: Paper & Envelope (detailed)

Everything in Envelop revolves around one analogy:

> **Paper + Envelope**

### Paper: the real message

- The actual business content:
  - “Hi Bob” in a chat
  - an API request
- In code, this is typically:
  - the `InnerPayload` of the *innermost* Envelope

### Envelope: responsible for delivery

Contains:

- recipient: `DestPeerID`
- sender: `ReturnPeerID`
- delivery rules: TTL / Flags
- content length: InnerLength
- content: InnerPayload (could be paper, could be another envelope)

> You can think of an Envelope as a “sealed letter with routing metadata written on the outside”.

### Forwarding mode: single-layer envelope

The simplest usage is **just one layer**:

1. Put Paper into an Envelope
2. Write Dest = B, Return = A
3. Send it into the network

Intermediate nodes:

- If they are **not** B:
  - see Dest = B → forward according to NextHop
- If they **are** B:
  - open the envelope → take out the paper → give it to the application (e.g. chat)

This is exactly what `SimpleStrategy` + Router do today.

### Onion mode: Paper → C → B → A

Multiple envelopes form an Onion:

1. Innermost: `Envelope C` (Dest = C)
2. Middle: `Envelope B` (InnerPayload = marshal(Envelope C), Dest = B)
3. Outermost: `Envelope A` (InnerPayload = marshal(Envelope B), Dest = A)

Delivery:

- You only send to A
- A opens its own envelope, finds an envelope to B inside → forwards to B
- B opens its own envelope, finds an envelope to C inside → forwards to C
- C opens its own envelope, finally sees the inner paper

Key points:

- A and B can only open the layer that has **their** name on it
- What they see is:
  - the next recipient
  - their own delivery metadata
- If the inner layers are encrypted, A and B cannot see the paper at all

In Envelop:

- The nesting logic is controlled by the Strategy:
  - how many layers
  - what Dest/Return to fill at each layer
- The Router only knows how to deliver an Envelope based on Dest/TTL
- After a node receives an Envelope addressed to itself, it can choose to:
  - open and see paper → deliver to the application
  - open and see another Envelope → give it back to Router → continue forwarding
  - re-wrap into new envelopes → send further

> **How to continue delivery is decided by the recipient.**  
> The protocol’s job is just to ensure “the envelope reaches the right DestPeerID”.

---

## 🚀 Quick Start

### Requirements

- Go 1.21+ (go.mod currently states 1.25; using a recent Go is recommended)

### Run the minimal demo in this repo

```bash
git clone https://github.com/yourname/envelop.git
cd envelop
go run .
```

The `main.go` at repo root currently:

- creates a Host
- listens on a local address (e.g. `0.0.0.0:9000`)
- sends one message to itself
- prints the received message from `Recv()`

### Use it in your own project

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
	// 1) Create a local Host. It will automatically initialize:
	//    - KeyPair (identity)
	//    - RelayRegistry (address store)
	//    - PeerManager (connection pool)
	//    - Router (routing)
	//    - Node (QUIC)
	//    - Strategy + Socket
	h, err := host.NewLocal("DemoNode", "0.0.0.0:9000")
	if err != nil {
		log.Fatal("NewLocal error:", err)
	}

	fmt.Println("My PeerID =", peer.PeerIDToDomain(h.ID()))
	fmt.Println("Listening on", h.Addr())

	// 2) Start the underlying QUIC listener
	go func() {
		if err := h.Start(); err != nil {
			log.Fatal("Host.Start error:", err)
		}
	}()

	// 3) Application receive loop
	go func() {
		for msg := range h.Recv() {
			fmt.Printf("[App] from %s: %s\n",
				peer.PeerIDToDomain(msg.From),
				string(msg.Payload),
			)
		}
	}()

	// 4) Demo: send a message to ourselves
	time.Sleep(time.Second)
	if err := h.Send(h.ID(), []byte("hello from Host API")); err != nil {
		log.Println("Send error:", err)
	}

	select {}
}
```

> ⚠️ This project is currently intended for **experimentation / learning**.  
> The protocol is still evolving; do not use it directly for production or security-sensitive environments.

---

## 📚 Core modules overview

### `envelop/` — Envelope v2

- Defines the Envelope structure and (un)marshalling
- Only cares “what the envelope looks like”, not what the paper says

### `frame/` — Frame v2

- Defines `Type + Length + Payload` frame format
- Used to carry individual messages over QUIC streams

### `peer/` — PeerID & KeyPair

- Manages node identities:
  - `KeyPair`: public/private keys
  - `PeerID`: identifier derived from the public key
- Provides helpers like `PeerIDToDomain`

### `netquic/` — QUIC node & connection management

- `Node`:
  - bound to a Router
  - responsible for:
    - starting a QUIC listener (ListenAndServe)
    - receiving Frames → parsing to Envelopes → handing them to Router
    - writing Router-produced Envelopes into QUIC streams
- `PeerManager`:
  - manages QUIC connections to peers
  - exposes `SendToPeer(peerID, env)` to upper layers
- `RelayRegistry`:
  - stores PeerID ↔ address mappings
  - supports static and dynamic registration, useful for NAT punch / relay

### `router/` — Router & RouteTable

- Decides where an Envelope goes based on DestPeerID / TTL
- Uses injected functions:
  - `NextHop`: how to find the next hop
  - `Send`: how to send to that next hop
- Supports callbacks:
  - REGISTER handling
  - OnPayload
  - OnRegister

### `strategy/` — EnvelopeStrategy

- Defines how a single layer of envelope is built/parsed:
  - `BuildOutgoing`
  - `HandleIncoming`
- Currently:
  - `SimpleStrategy`: single-layer demo strategy

### `socket/` — EnvelopSocket

- Exposes:
  - `Send(dest, payload)`
  - `Recv() <-chan IncomingMessage`
- Bridges Strategy + Router underneath

### `host/` — Host & Builder

- High-level wrapper:
  - `NewLocal(name, listenAddr)`: one-liner to create a node
  - `Host.Start()`: start the node
  - `Host.Send / Host.Recv`: app-level APIs
- Composes Node / Router / PeerManager / Registry / Strategy / Socket into one object

---

## 📁 Repository structure

```text
envelop/
  go.mod
  main.go              # Minimal demo: Host.NewLocal + send to self

  envelop/             # Envelope v2 definition & (un)marshalling
  frame/               # Frame v2
  peer/                # KeyPair / PeerID
  netquic/             # QUIC Node / PeerManager / RelayRegistry
  router/              # Router / RouteTable / DHT primitives
  strategy/            # EnvelopeStrategy interface + SimpleStrategy
  socket/              # EnvelopSocket: Send/Recv Facade
  host/                # Host + Builder: high-level wrapper
```

(You may also add:)

```text
  docs/
    architecture.md    # (optional) high-level architecture
    protocol.md        # (optional) Frame / Envelope binary formats
    roadmap.md         # (optional) future technical plans (.p2p / .env / proxy integration, etc.)
```

---

## 🛣️ Roadmap (technical ideas only)

> The following are technical directions for future work, for reference only.

- **Protocol layer**
  - Document Frame v2 / Envelope v2 formats (`docs/protocol.md`)
  - Define Flags semantics: REGISTER / Encrypted / Control / …

- **Security & anonymity**
  - Implement real multi-layer Onion encryption (per-hop keys)
  - Add MAC / signatures to Envelope for tamper-resistance and replay protection

- **Routing & DHT**
  - Build a full Kademlia routing layer on the current RouteTable / FIND_NODE
  - Support multiple NextHop strategies (direct / relay / hybrid)

- **HTTP / proxy integration**
  - Build a local client proxy on top of Host (HTTP/HTTPS → Envelop)
  - Provide simple integration examples with Nginx / Caddy

---

## 🪪 License

This project is licensed under the **GNU Affero General Public License v3.0 (AGPLv3)**.  
See the [`LICENSE`](LICENSE) file in the repository for details.
