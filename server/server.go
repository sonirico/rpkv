// Package server is the HTTP query surface serving Get-by-key over the
// index and the log, per SPEC.
package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/sonirico/rpkv/clock"
)

const (
	supersedeBudget        = 2 * time.Second
	checkpointPollInterval = 50 * time.Millisecond
)

// Server serves the rpkv HTTP query surface over a set of per-topic
// backends.
type Server struct {
	backends map[string]Backend
	clk      clock.Clock
	logger   *slog.Logger
	mux      *http.ServeMux
}

// NewServer wires the per-topic backends, clock and logger into a Server
// and registers its routes.
func NewServer(backends map[string]Backend, clk clock.Clock, logger *slog.Logger) *Server {
	s := &Server{
		backends: backends,
		clk:      clk,
		logger:   logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/kv/{topic}/{key}", s.handleKV)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux = mux

	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}
