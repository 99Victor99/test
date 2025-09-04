# 构建阶段
FROM --platform=linux/amd64 golang:1.22 AS builder

WORKDIR /app
COPY go.mod ./
COPY main.go ./

# 静态构建
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -installsuffix cgo -o test-app

# 运行阶段
FROM --platform=linux/amd64 debian:bullseye-slim
WORKDIR /app
COPY --from=builder /app/test-app .
EXPOSE 8080
CMD ["./test-app"]

