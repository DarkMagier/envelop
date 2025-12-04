package main

import (
	"fmt"
	"log"
	"time"

	"envelop/host"
	"envelop/peer"
)

func main() {
	// 1）创建一个本地 Host（自动生成 PeerID / Registry / Router / Node / Socket）
	h, err := host.NewLocal("DemoNode", "0.0.0.0:9000")
	if err != nil {
		log.Fatal("NewLocal error:", err)
	}

	fmt.Println("My PeerID =", peer.PeerIDToDomain(h.ID()))
	fmt.Println("Listening on", h.Addr())

	// 2）启动监听（底层 Node.ListenAndServe）
	go func() {
		if err := h.Start(); err != nil {
			log.Fatal("Host.Start error:", err)
		}
	}()

	// 3）启动接收循环（应用逻辑）
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
