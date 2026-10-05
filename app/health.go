package app

import "net/http"

func healthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeErrorResponse(w, map[string]any{"status": "ok"}, http.StatusOK)
	}
}
