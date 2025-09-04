package main

import (
	"go.uber.org/zap"
	"test/common"
	"time"
)

func main1() {
	// 生产环境
	{
		logger, _ := zap.NewProduction()
		defer logger.Sync() // 刷新 buffer，保证日志最终会被输出

		url := "https://jianghushinian.cn/"
		logger.Info("production failed to fetch URL",
			zap.String("url", url), // 因为没有使用 interface{} 和反射机制，所以需要指定具体类型
			zap.Int("attempt", 3),
			zap.Duration("backoff", time.Second),
		)
	}

	// 开发环境
	{
		logger, _ := zap.NewDevelopment()
		defer logger.Sync()

		url := "https://jianghushinian.cn/"
		logger.Debug("development failed to fetch URL",
			zap.String("url", url),
			zap.Int("attempt", 3),
			zap.Duration("backoff", time.Second),
		)
	}

	common.LogOut()
}
