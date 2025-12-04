package main

import (
	"log"
	"time"

	"envelop/envelop"
	"envelop/netquic"
	"envelop/peer"
	"envelop/router"
	"envelop/strategy"
)

const (
	RPC_ECHO      = "RPC_ECHO"
	RPC_ECHO_RESP = "RPC_ECHO_RESP"
)

func main() {

	/****************************************************
	 * 1. 三个节点：Alice → Relay → Bob
	 ****************************************************/
	aliceKP, _ := peer.NewKeyPair()
	relayKP, _ := peer.NewKeyPair()
	bobKP, _ := peer.NewKeyPair()

	registry := netquic.NewRelayRegistry()
	rt := router.NewRouteTable()

	// 静态可达
	registry.RegisterStatic(relayKP.PeerID, "127.0.0.1:9401")
	registry.RegisterStatic(bobKP.PeerID, "127.0.0.1:9402")
	registry.RegisterStatic(aliceKP.PeerID, "127.0.0.1:9403")

	rt.LearnDirect(relayKP.PeerID)
	rt.LearnDirect(bobKP.PeerID)
	rt.LearnDirect(aliceKP.PeerID)

	onion := strategy.NewOnionStrategy()

	/****************************************************
	 * 2. Relay：只做拆一层 + 转发
	 ****************************************************/
	relayNode := &netquic.Node{
		Name:     "Relay",
		Key:      relayKP,
		PeerMgr:  netquic.NewPeerManager(registry.Resolver),
		Registry: registry,
	}

	relayRouter := &router.Router{
		SelfID:     relayKP.PeerID,
		RouteTable: rt,
		NextHop:    func(dest peer.PeerID) (peer.PeerID, bool) { return dest, true },
		Send: func(nextHop peer.PeerID, env *envelop.Envelope) {
			log.Printf("[Relay Router] 转发到 %s", peer.PeerIDToDomain(nextHop))
			relayNode.PeerMgr.SendToPeer(nextHop, env)
		},
		OnPayload: func(env *envelop.Envelope) {
			// Relay 业务：不处理
		},
	}

	relayNode.Router = relayRouter

	/****************************************************
	 * 3. Bob：处理 RPC + 回包（也用 Onion）
	 ****************************************************/
	bobNode := &netquic.Node{
		Name:     "Bob",
		Key:      bobKP,
		PeerMgr:  netquic.NewPeerManager(registry.Resolver),
		Registry: registry,
	}

	bobNode.Router = &router.Router{
		SelfID: bobKP.PeerID,
		OnPayload: func(env *envelop.Envelope) {

			msg := string(env.InnerPayload)
			log.Printf("[Bob] OnPayload 收到: %q", msg)

			if len(msg) >= len(RPC_ECHO)+1 &&
				msg[:len(RPC_ECHO)] == RPC_ECHO {

				payload := msg[len(RPC_ECHO)+1:] // 去掉 RPC_ECHO:
				log.Printf("[Bob] 识别到 RPC_ECHO 请求，内容 = %q", payload)

				respPayload := []byte(RPC_ECHO_RESP + ":" + payload)

				// 使用 OnionStrategy → Bob → Relay → Alice
				respEnv, _ := onion.BuildEnvelope(
					env.ReturnPeerID, // dest = Alice
					bobKP.PeerID,     // from
					respPayload,
					[]peer.PeerID{relayKP.PeerID},
				)

				log.Printf("[Bob] 准备回 RPC 响应给 %s", peer.PeerIDToDomain(env.ReturnPeerID))
				bobNode.PeerMgr.SendToPeer(relayKP.PeerID, respEnv)
				log.Println("[Bob] RPC 响应已发送")
			}
		},
	}

	/****************************************************
	 * 4. Alice：发 Onion RPC
	 ****************************************************/
	aliceNode := &netquic.Node{
		Name:     "Alice",
		Key:      aliceKP,
		PeerMgr:  netquic.NewPeerManager(registry.Resolver),
		Registry: registry,
	}

	aliceNode.Router = &router.Router{
		SelfID: aliceKP.PeerID,
		OnPayload: func(env *envelop.Envelope) {
			msg := string(env.InnerPayload)
			log.Printf("[Alice] OnPayload 收到: %q", msg)

			if len(msg) >= len(RPC_ECHO_RESP)+1 &&
				msg[:len(RPC_ECHO_RESP)] == RPC_ECHO_RESP {

				payload := msg[len(RPC_ECHO_RESP)+1:]
				log.Printf("[Alice] 收到 RPC_ECHO 响应，结果 = %q", payload)
			}
		},
	}

	/****************************************************
	 * 5. 启动所有节点
	 ****************************************************/
	go relayNode.ListenAndServe("0.0.0.0:9401")
	go bobNode.ListenAndServe("0.0.0.0:9402")
	go aliceNode.ListenAndServe("0.0.0.0:9403")
	time.Sleep(time.Second)

	/****************************************************
	 * 6. Alice → Onion RPC → Bob
	 ****************************************************/
	log.Printf("[Alice] PeerID = %s", peer.PeerIDToDomain(aliceKP.PeerID))
	log.Printf("[Relay] PeerID = %s", peer.PeerIDToDomain(relayKP.PeerID))
	log.Printf("[Bob]   PeerID = %s", peer.PeerIDToDomain(bobKP.PeerID))

	payload := []byte(RPC_ECHO + ":Hello Bob, this is Alice via Onion RPC!")

	// 构造 Onion RPC：path = [Relay]
	reqEnv, _ := onion.BuildEnvelope(
		bobKP.PeerID,   // final dest = Bob
		aliceKP.PeerID, // Alice
		payload,
		[]peer.PeerID{relayKP.PeerID},
	)

	log.Printf("[Alice] 准备发送 Onion RPC：外层 Dest = %s",
		peer.PeerIDToDomain(relayKP.PeerID),
	)

	aliceNode.PeerMgr.SendToPeer(relayKP.PeerID, reqEnv)
	log.Println("[Alice] Onion RPC 已发出")

	time.Sleep(3 * time.Second)
	log.Println("==== Onion RPC Demo 结束 ====")
}
