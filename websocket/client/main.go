package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"sync/atomic"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

type SafeChan struct {
	ch     chan int
	closed atomic.Bool
}

func main() {
	// 设置 WebSocket 服务器的地址
	serverURL := url.URL{Scheme: "ws", Host: "localhost:8080", Path: "/ws"}
	fmt.Printf("Connecting to %s\n", serverURL.String())

	// 连接到 WebSocket 服务器
	conn, _, _, err := ws.DefaultDialer.Dial(context.Background(), serverURL.String())
	if err != nil {
		log.Fatal("Failed to connect:", err)
	}
	defer conn.Close()

	fmt.Println("Connected to WebSocket server.")

	// 启动一个 goroutine 来读取来自服务器的消息
	go func() {
		for {
			// 读取服务器消息
			msg, op, err := wsutil.ReadServerData(conn)
			if err != nil {
				log.Fatal("Failed to read message:", err)
			}
			if op == ws.OpClose {
				fmt.Println("Server closed the connection.")
				break
			}
			fmt.Printf("Received from server: %s\n", string(msg))
		}
	}()

	// 创建一个循环，不断从命令行输入消息并发送
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("Enter message to send: ")
		text, _ := reader.ReadString('\n')
		text = strings.TrimSpace(text)

		// 检查是否输入 "exit" 退出循环
		if text == "exit" {
			fmt.Println("Closing connection...")
			wsutil.WriteClientMessage(conn, ws.OpClose, nil)
			break
		}

		// 发送文本消息到服务器
		err = wsutil.WriteClientMessage(conn, ws.OpText, []byte(text))
		if err != nil {
			log.Fatal("Failed to send message:", err)
		}
	}
}
