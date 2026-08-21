// Package router is a thin proxy that answers Get-by-key by asking every
// shard, never reproducing the producer's partitioner.
package router

import (
	"log/slog"
	"net/http"

	"github.com/sonirico/rpkv/metrics"
)

// Router fans a Get-by-key request out to every shard and arbitrates the
// responses.
type Router struct {
	shards  []string
	client  *http.Client
	logger  *slog.Logger
	metrics Metrics
	mux     *http.ServeMux
}

// New wires the shard addresses, HTTP client and logger into a Router and
// registers its routes.
func New(
	shards []string,
	client *http.Client,
	logger *slog.Logger,
	opts ...Option,
) *Router {
	s := &Router{
		shards: shards,
		client: client,
		logger: logger,
		metrics: Metrics{
			AmbiguousKeys: metrics.NewNoopCounter(),
		},
	}
	for _, opt := range opts {
		opt(s)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/kv/{topic}/{key}", s.handleKV)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux = mux

	return s
}

// ServeHTTP implements http.Handler.
func (s *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}
