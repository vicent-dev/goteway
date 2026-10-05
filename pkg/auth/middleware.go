package auth

import (
	"net/http"
)

type Authenticator interface {
	Authenticate(bearer string) (*Principal, error)
}

type ErrorHandler func(w http.ResponseWriter, r *http.Request, err error)

func RequireAuth(authenticator Authenticator, onError ErrorHandler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			bearer, err := BearerToken(r.Header)
			if err == nil {
				var principal *Principal
				principal, err = authenticator.Authenticate(bearer)
				if err == nil {
					next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), principal)))
					return
				}
			}
			onError(w, r, err)
		})
	}
}
