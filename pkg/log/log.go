package log

import (
	"context"
	"fmt"

	"github.com/en-vee/alog"
)

type ContextLogKey string

const METHOD_LOG_KEY = ContextLogKey("method")
const PATH_LOG_KEY = ContextLogKey("path")

func LogError(ctx context.Context, l string) {
	alog.Error(fmt.Sprintf("[%v] - %v: %v", ctx.Value(METHOD_LOG_KEY), ctx.Value(PATH_LOG_KEY), l))
}

func LogWarn(ctx context.Context, l string) {
	alog.Warn(fmt.Sprintf("[%v] - %v: %v", ctx.Value(METHOD_LOG_KEY), ctx.Value(PATH_LOG_KEY), l))
}

func LogInfo(ctx context.Context, l string) {
	alog.Info(fmt.Sprintf("[%v] - %v: %v", ctx.Value(METHOD_LOG_KEY), ctx.Value(PATH_LOG_KEY), l))
}
