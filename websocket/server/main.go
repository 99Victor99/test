package main

import (
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"log"
	"net"
)

func main() {
	// 在本地端口 8080 上监听 TCP 连接
	ln, err := net.Listen("tcp", ":8080")
	if err != nil {
		log.Fatal(err)
	}

	for {
		// 接受客户端的连接
		conn, err := ln.Accept()
		if err != nil {
			log.Fatal(err)
		}

		// 协议升级，建立 WebSocket 连接
		_, err = ws.Upgrade(conn)
		if err != nil {
			log.Println("Upgrade error:", err)
			conn.Close()
			continue
		}

		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()

	for {
		// 读取客户端消息
		msg, op, err := wsutil.ReadClientData(conn)
		if err != nil {
			log.Println("Read error:", err)
			return
		}

		log.Printf("Received: %s\n", string(msg))

		// 回复消息
		err = wsutil.WriteServerMessage(conn, op, []byte("Hello from server! "+string(msg)))
		if err != nil {
			log.Println("Write error:", err)
			return
		}
	}
}
