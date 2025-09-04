package common

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"os"
	"time"
)

func LogOut() {
	// 定义 EncoderConfig 配置
	encoderConfig := zapcore.EncoderConfig{
		MessageKey:       "message",                     // 输出消息字段 "message":
		LevelKey:         "level",                       // 日志级别 "level":
		TimeKey:          "time",                        // 时间戳 "time":
		NameKey:          "logger",                      // 记录器名称key 必须logger.Named("myLogger") 为日志记录器命名才能输出
		CallerKey:        "caller",                      // 调用者信息（文件名和行号）"caller":"common/common.go:41"
		FunctionKey:      "func",                        // 调用的函数名称 "func":"test/common.LogOut"
		StacktraceKey:    "stacktrace",                  // 堆栈跟踪key 输出堆栈跟踪 空则不输出
		LineEnding:       zapcore.DefaultLineEnding,     // 换行符 默认\n
		EncodeLevel:      zapcore.CapitalLevelEncoder,   // 级别大写输出; 控制日志级别LevelKey的值INFO ERROR等大小写
		EncodeTime:       zapcore.ISO8601TimeEncoder,    // ISO8601 时间格式 控制TimeKey的输出格式
		EncodeDuration:   zapcore.StringDurationEncoder, // 持续时间格式化为字符串 控制zap.Duration("elapsed", duration) 输出可视化 2h35m
		EncodeCaller:     zapcore.ShortCallerEncoder,    // 调用者文件路径短格式 控制CallerKey的输出格式
		EncodeName:       zapcore.FullNameEncoder,       // 记录器名称全名 默认
		ConsoleSeparator: "\t",                          // 使用制表符分隔输出 默认\t
	}

	// 创建 JSON 编码器
	encoder := zapcore.NewJSONEncoder(encoderConfig)

	// 设置输出目标
	core := zapcore.NewCore(encoder, zapcore.AddSync(os.Stdout), zapcore.DebugLevel)
	// zapcore.AddSync() 将日志写到指定的输出流
	// zapcore.DebugLevel 设置日志级别, 只有日志级别>= DebugLevel 的日志才会输出; DebugLevel < InfoLevel < WarnLevel < ErrorLevel

	// 创建 Logger 实例
	// New(core zapcore.Core, options ...Option) *Logger
	logger := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
	// 要加上 AddCaller() 才能显示文件名和行号
	// AddStacktrace() error时才能显示堆栈信息

	// 追加core配置固定输出字段
	logger = logger.With(zap.String("extra_key", "extra_value"))

	// *****无糖输出******
	// 输出field包含任意类型, 每个字段一个函数, 不包含infof, errorf等
	logger.Info("info! This is an info message",
		zap.String("key", "value"),
		zap.Strings("name", []string{"name1", "name2"}),
		zap.Int("int", 1),
		zap.Ints("int32", []int{1, 2, 3}),
		zap.Bool("bool", true),
		zap.Time("timestamp", time.Now()),
		zap.Any("interface", map[string]interface{}{"name": "Tom"}),
		zap.Duration("elapsed", time.Second), // 输出可视化 1s
	)
	logger.Debug("debug! This is an info message", zap.String("key", "value"))
	logger.Error("error! This is an error message", zap.Time("timestamp", time.Now())) // 输出堆栈信息

	{
		logger, _ := zap.NewProduction()
		defer logger.Sync()

		url := "https://jianghushinian.cn/"
		sugar := logger.Sugar()
		sugar.Infow("production failed to fetch URL",
			"url", url,
			"attempt", 3,
			"backoff", time.Second,
		)
		sugar.Info("Info")
		sugar.Infof("Infof: %s", url)
		sugar.Infoln("Infoln")
	}
}
