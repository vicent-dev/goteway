package auth

import "context"

type contextAuthKey string

var BEARER_TOKEN_HEADER_KEY = "Authorization"

const AUTH_CTX_KEY = contextAuthKey("auth")

func IsValidToken(ctx context.Context) bool {
	// @todo implement token validation
	return ctx.Value(AUTH_CTX_KEY) != ""
}
