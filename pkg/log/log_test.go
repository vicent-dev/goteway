package log

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestContextKeys(t *testing.T) {
	ctx := context.Background()
	ctx = context.WithValue(ctx, METHOD_CTX_LOG_KEY, "GET")
	ctx = context.WithValue(ctx, PATH_CTX_LOG_KEY, "/api/test")

	method := ctx.Value(METHOD_CTX_LOG_KEY)
	path := ctx.Value(PATH_CTX_LOG_KEY)

	assert.Equal(t, "GET", method)
	assert.Equal(t, "/api/test", path)
}
