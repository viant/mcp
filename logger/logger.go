package logger

import "context"

type Logger interface {
	Debug(ctx context.Context, data interface{}) error
	Info(ctx context.Context, data interface{}) error
	Notice(ctx context.Context, data interface{}) error
	Warning(ctx context.Context, data interface{}) error
	Error(ctx context.Context, data interface{}) error
	Critical(ctx context.Context, data interface{}) error
	Alert(ctx context.Context, data interface{}) error
	Emergency(ctx context.Context, data interface{}) error
	Logger(name string) Logger
}
