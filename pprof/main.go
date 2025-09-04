package main

/*func main() {
	// 创建并监听 gops agent，gops 命令会通过连接 agent 来读取进程信息
	// 若需要远程访问，可配置 agent.Options{Addr: "0.0.0.0:6060"}，否则默认仅允许本地访问
	if err := agent.Listen(agent.Options{}); err != nil {
		log.Fatalf("agent.Listen err: %v", err)
	}

	http.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`Go 语言编程之旅 `))
	})
	http.ListenAndServe(":6060", http.DefaultServeMux)
}*/

import (
	"log"
	"net/http"
	_ "net/http/pprof" // This registers the pprof handlers
	"runtime"
)

func init() {
	runtime.SetMutexProfileFraction(1)
	runtime.SetBlockProfileRate(1) // 启用阻塞分析
}

var datas []string

func main() {
	go func() {
		for {
			log.Printf("len: %d", Add("go-programming-tour-book"))
			//time.Sleep(time.Millisecond * 1)
		}
	}()

	_ = http.ListenAndServe("0.0.0.0:6060", nil)
}

func Add(str string) int {
	data := []byte(str)
	datas = append(datas, string(data))
	return len(datas)
}

//func main() {
//	trace.Start(os.Stderr)
//	defer trace.Stop()
//
//	ch := make(chan string)
//	go func() {
//		ch <- "Go 语言编程之旅"
//	}()
//
//	<-ch
//}
