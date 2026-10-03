package auth

import "context"

type contextAuthKey string

var BEARER_TOKEN_HEADER_KEY = "Authorization"

const AUTH_CTX_KEY = contextAuthKey("auth")

func IsValidToken(ctx context.Context) bool {
	// @todo implement token validation
	v := ctx.Value(AUTH_CTX_KEY)
	if v == nil {
		return false
	}
	if s, ok := v.(string); ok {
		return s != ""
	}
	return false
}
