package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/gorilla/mux"
)

func main() {
	r := mux.NewRouter()

	// 定义一个 HTTP 端点
	r.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "Hello from Service A-01, port 8001!")
	})

	// 启动 HTTP 服务器
	log.Println("Service A is running on :8001...")
	log.Fatal(http.ListenAndServe(":8001", r))
}
