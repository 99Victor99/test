package main

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
	"time"
)

func main() {
	// 创建一个 lumberjack.Logger，用于日志滚动
	lumberjackLogger := &lumberjack.Logger{
		Filename:   "log.log", // 日志文件名
		MaxSize:    1,         // 单个日志文件最大大小（单位：MB）
		MaxBackups: 3,         // 保留的旧日志文件个数
		MaxAge:     7,         // 日志文件最多保存天数
		Compress:   true,      // 是否压缩旧的日志文件
	}

	// 创建Zap logger配置
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "time"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	// 缓冲
	bufferedWriteSyncer := &zapcore.BufferedWriteSyncer{
		WS:            zapcore.AddSync(lumberjackLogger),
		Size:          1024, // 1024 B
		FlushInterval: time.Second * 5,
	}

	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig), // 输出格式为JSON
		bufferedWriteSyncer,                   // 使用BufferedWriteSyncer
		zapcore.InfoLevel,                     // 日志级别
	)

	logger := zap.New(core)

	// 示例日志输出
	for i := 0; i < 10000; i++ {
		logger.Info("Logging with buffer and rotationttttyyyyyyyyyyyyyyyyyrotationttttyyyyyyyyyyyyyyyyyrotationttttyyyyyyyyyyyyyyyyyrotationttttyyyyyyy----------",
			zap.Int("count", i))
		//time.Sleep(time.Second)
	}

	//logger.Sync()
}
