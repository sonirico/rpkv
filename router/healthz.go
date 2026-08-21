package router

import (
	"encoding/json"
	"net/http"
)

type healthResponse struct {
	Mode   string   `json:"mode"`
	Shards []string `json:"shards"`
}

// handleHealthz serves GET /healthz: the router's mode and the configured
// shard addresses.
func (s *Router) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(healthResponse{
		Mode:   "router",
		Shards: s.shards,
	}); err != nil {
		s.logger.Error("encode healthz response", "error", err)
	}
}
