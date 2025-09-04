.PHONY: all client server clean

PROTOC_GEN_GO := $(shell which protoc-gen-go)
PROTOC_GEN_GO_GRPC := $(shell which protoc-gen-go-grpc)

all: client server

client:
	protoc \
		-I=proto \
		--go_out=client/gen \
		--go-grpc_out=client/gen \
		proto/hello.proto

server:
	protoc \
		-I=proto \
		--go_out=server/gen \
		--go-grpc_out=server/gen \
		proto/hello.proto

clean:
	rm -rf client/gen/* server/gen/*

PROTOC_GEN_GO := $(shell which protoc-gen-go)
PROTOC_GEN_GO_GRPC := $(shell which protoc-gen-go-grpc)

all: client server

client:
	protoc \
		-I=proto \
		--go_out=client/gen \
		--go-grpc_out=client/gen \
		proto/hello.proto

server:
	protoc \
		-I=proto \
		--go_out=server/gen \
		--go-grpc_out=server/gen \
		proto/hello.proto

clean:
	rm -rf client/gen/* server/gen/*

