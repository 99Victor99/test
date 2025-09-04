package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"

	"github.com/gorilla/mux"
)

func main() {
	r := mux.NewRouter()

	// 定义一个 HTTP 端点，调用 Service A
	r.HandleFunc("/call-service-a", func(w http.ResponseWriter, r *http.Request) {
		// 使用 Dapr 的服务调用功能调用 Service A
		resp, err := http.Get("http://localhost:3511/v1.0/invoke/service-a/method/hello")
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to call Service A: %v", err), http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to read response: %v", err), http.StatusInternalServerError)
			return
		}

		fmt.Fprintf(w, "Response from Service A: %s", string(body))
	})

	// 启动 HTTP 服务器
	log.Println("Service B is running on :8081...")
	log.Fatal(http.ListenAndServe(":8081", r))
}
