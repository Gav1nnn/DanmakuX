package logger

import "go.uber.org/zap"

// New 创建生产环境日志实例（JSON 输出，结构化字段）。
func New() (*zap.Logger, error) {
	return zap.NewProduction()
}
