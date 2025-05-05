package server

import (
	"context"
	"encoding/json"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp-protocol/logger"
	"github.com/viant/mcp-protocol/schema"
)

type Logger struct {
	name     string
	level    *schema.LoggingLevel
	notifier transport.Notifier
}

// Logger creates a new logger with a name
func (l *Logger) Logger(name string) logger.Logger {
	return &Logger{
		name:     name,
		level:    l.level,
		notifier: l.notifier,
	}
}

func (l *Logger) log(ctx context.Context, level schema.LoggingLevel, data any) error {
	if l.level == nil || l.level.Ordinal() > level.Ordinal() {
		//skip logging since level is too verbose
		return nil
	}
	request := &jsonrpc.Notification{Method: schema.MethodNotificationMessage}
	params := schema.LoggingMessageNotificationParams{
		Level:  level,
		Logger: &l.name,
		Data:   data,
	}
	var err error
	request.Params, err = json.Marshal(params)
	if err != nil {
		return err
	}
	return l.notifier.Notify(ctx, request)
}

func (l *Logger) Debug(ctx context.Context, data interface{}) error {
	return l.log(ctx, schema.LoggingLevelDebug, data)
}

func (l *Logger) Info(ctx context.Context, data interface{}) error {
	return l.log(ctx, schema.Info, data)
}

func (l *Logger) Notice(ctx context.Context, data interface{}) error {
	return l.log(ctx, schema.Notice, data)
}

func (l *Logger) Warning(ctx context.Context, data interface{}) error {
	return l.log(ctx, schema.Warning, data)
}

func (l *Logger) Error(ctx context.Context, data interface{}) error {
	return l.log(ctx, schema.Error, data)
}

func (l *Logger) Critical(ctx context.Context, data interface{}) error {
	return l.log(ctx, schema.Critical, data)
}

func (l *Logger) Alert(ctx context.Context, data interface{}) error {
	return l.log(ctx, schema.Alert, data)
}

func (l *Logger) Emergency(ctx context.Context, data interface{}) error {
	return l.log(ctx, schema.Emergency, data)
}

func NewLogger(name string, level *schema.LoggingLevel, notifier transport.Notifier) *Logger {
	return &Logger{
		name:     name,
		level:    level,
		notifier: notifier,
	}
}
