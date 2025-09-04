package main

import (
	"fmt"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"os"
	"time"
)

func main() {
	// 自定义Encoder配置
	encoderConfig := zapcore.EncoderConfig{}

	// 创建一个JSON格式的encoder
	encoder := zapcore.NewJSONEncoder(encoderConfig)

	// 设置日志级别
	level := zapcore.InfoLevel

	// 缓冲
	bufferedWriteSyncer := &zapcore.BufferedWriteSyncer{
		WS:            os.Stderr,
		Size:          1024, // 1024 B
		FlushInterval: time.Second * 5,
	}

	// 创建一个输出目标（标准输出）
	core := zapcore.NewCore(encoder, bufferedWriteSyncer, level)
	//
	//// 创建Logger
	logger := zap.New(core)

	sugar := logger.Sugar()
	//
	//// 示例日志输出
	for i := 0; i < 10; i++ {
		sugar.Infow("failed to fetch URL",
			// Structured context as loosely typed key-value pairs.
			"url", "aaaaa",
			"attempt", 3,
			"backoff", time.Second,
		)
		//logger.Info("Logging with buffer and rotation",
		//	zap.Int("count", i))
		//time.Sleep(time.Second)
	}
	time.Sleep(time.Second * 25)
	fmt.Println("++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++")
	time.Sleep(time.Second * 60)

	// 确保日志输出被刷新
	//defer logger.Sync()
}
