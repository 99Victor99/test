package main

import (
	"fmt"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"os"
	"time"
)

func main() {
	logger := getLogger()
	defer logger.Sync()

	logger.Info("This is an info log")
	logger.Warn("This is a warning log")
	logger.Error("This is an error log")
}

// 创建 logger 并设置输出到文件
func getLogger() *zap.Logger {
	// 获取当前工作目录
	currentDir, err := os.Getwd()
	if err != nil {
		fmt.Println("Error getting current directory:", err)
	}

	// 输出当前工作目录
	fmt.Println("Current Directory:", currentDir)
	// 手动按日分割日志
	fileName := "./logs/" + time.Now().Format("2006-01-02") + "-app.log"
	file, err := os.OpenFile(fileName, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0777) //参数3只要在linux系统下才会生效

	if err != nil {
		panic(err)
	}

	// 创建日志的编码器配置
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder, // 日志等级小写输出
		EncodeTime:     zapcore.ISO8601TimeEncoder,    // ISO8601 时间格式
		EncodeDuration: zapcore.StringDurationEncoder, // 输出持续时间的字符串格式
		EncodeCaller:   zapcore.ShortCallerEncoder,    // 输出调用文件名和行号
	}

	// 创建核心，设置日志级别为 Info
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig),              // JSON 格式化日志
		zapcore.NewMultiWriteSyncer(zapcore.AddSync(file)), // 输出到文件
		zap.InfoLevel, // 日志级别
	)

	// 创建 logger
	return zap.New(core, zap.AddCaller())

}
