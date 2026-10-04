package app

import "net/http"

// healthHandler reports that the process is serving. It inspects nothing, on
// purpose: the one dependency the gateway cannot start without is already
// required to boot, and the other is built to fail open, so a probe that checked
// them could only repeat what start-up proved.
//
// It is a liveness signal, not a readiness one. Nothing here can tell an
// orchestrator that postgres became unreachable after the process came up.
func healthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeErrorResponse(w, map[string]any{"status": "ok"}, http.StatusOK)
	}
}
