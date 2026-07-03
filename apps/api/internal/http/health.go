package http

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
)

// healthResponse is the JSON body returned by liveness/readiness endpoints.
type healthResponse struct {
	Status    string `json:"status"`
	Service   string `json:"service"`
	RequestID string `json:"request_id,omitempty"`
}

// handleHealth reports process liveness. It intentionally has no external
// dependencies so it stays green even when Postgres/Redis are still starting;
// readiness (which will check dependencies) arrives in a later story.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{
		Status:    "ok",
		Service:   "tunnex-api",
		RequestID: middleware.GetReqID(r.Context()),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
