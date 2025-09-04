package main

import (
	"context"
	"google.golang.org/grpc"
	"log"
	"net"
	"net/http"
	"sync"
	"test/pb"
)

// gRPC Service Definition
type GoodsService struct{}

var GoodsServices = GoodsService{}

func (s *GoodsService) SayHello(ctx context.Context, req *HelloRequest) (*HelloResponse, error) {
	return &HelloResponse{Message: "Hello, " + req.Name}, nil
}

// gRPC Protobuf Definitions (normally in a .proto file)
type HelloRequest struct {
	Name string
}

type HelloResponse struct {
	Message string
}

func main() {
	var wg sync.WaitGroup
	wg.Add(2)

	// Start HTTP server
	go func() {
		defer wg.Done()
		http.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("Hello, HTTP!"))
		})
		log.Println("Starting HTTP server on :3500")
		if err := http.ListenAndServe(":3500", nil); err != nil {
			log.Fatalf("Failed to start HTTP server: %v", err)
		}
	}()

	// Start gRPC server
	go func() {
		defer wg.Done()
		s := grpc.NewServer()
		listener, err := net.Listen("tcp", ":3501")
		pb.RegisterGoodsServiceServer(s, GoodsServices)

		if err != nil {
			log.Fatalf("Failed to listen on port 3501: %v", err)
		}
		log.Println("Starting gRPC server on :3501")
		if err := grpcServer.Serve(listener); err != nil {
			log.Fatalf("Failed to start gRPC server: %v", err)
		}
	}()

	// Wait for both servers to start
	wg.Wait()
}
