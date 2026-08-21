package server

import (
	"net/http"
	"strconv"

	"github.com/sonirico/rpkv/fetch"
	"github.com/sonirico/rpkv/index"
)

// handleKV serves GET /v1/kv/{topic}/{key} per SPEC's response table.
func (s *Server) handleKV(w http.ResponseWriter, r *http.Request) {
	start := s.clk.Now()
	defer func() {
		s.metrics.RequestDuration.Observe(s.clk.Now().Sub(start).Seconds())
	}()

	b, ok := s.backends[r.PathValue("topic")]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		if _, err := w.Write([]byte("topic not indexed")); err != nil {
			s.logger.Error("write response", "error", err)
		}
		return
	}

	key, err := decodeKey(r)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	lk, err := b.index.Get(key)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !lk.Found {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	res, err := b.fetcher.FetchAt(r.Context(), lk.Pointer, key)
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	switch {
	case res.Evicted:
		w.WriteHeader(http.StatusGone)
	case res.Superseded:
		s.resolveSuperseded(w, r, b, key, lk.Pointer)
	default:
		s.writeHit(w, r, b, lk.Pointer, res)
	}
}

// writeHit writes a successful lookup: the raw value bytes plus the
// pointer and checkpoint headers.
func (s *Server) writeHit(
	w http.ResponseWriter,
	r *http.Request,
	b Backend,
	ptr index.Pointer,
	res fetch.Result,
) {
	cp, err := b.index.Checkpoint(ptr.Partition)
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	w.Header().Set("X-Rpkv-Partition", strconv.FormatInt(int64(ptr.Partition), 10))
	w.Header().Set("X-Rpkv-Offset", strconv.FormatInt(ptr.Offset, 10))
	w.Header().Set("X-Rpkv-Checkpoint", strconv.FormatInt(cp, 10))
	w.Header().Set("X-Rpkv-Timestamp", strconv.FormatInt(res.Timestamp.UnixMilli(), 10))
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(res.Value); err != nil {
		s.logger.Error("write value", "error", err)
	}
}

// resolveSuperseded implements the frozen supersede loop: poll the
// checkpoint until it passes the stale pointer's offset, then re-resolve
// the key and retry, bounded by supersedeBudget.
func (s *Server) resolveSuperseded(
	w http.ResponseWriter,
	r *http.Request,
	b Backend,
	key []byte,
	ptr index.Pointer,
) {
	deadline := s.clk.Now().Add(supersedeBudget)
	for {
		if !s.clk.Now().Before(deadline) {
			s.writeBudgetExhausted(w)
			return
		}

		select {
		case <-r.Context().Done():
			s.writeBudgetExhausted(w)
			return
		case <-s.clk.After(checkpointPollInterval):
		}
		s.metrics.SupersedeRetries.Inc()

		cp, err := b.index.Checkpoint(ptr.Partition)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		if cp <= ptr.Offset {
			continue
		}

		lk, err := b.index.Get(key)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		if !lk.Found {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if lk.Pointer == ptr {
			continue
		}

		res, err := b.fetcher.FetchAt(r.Context(), lk.Pointer, key)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		if res.Evicted {
			w.WriteHeader(http.StatusGone)
			return
		}
		if res.Superseded {
			ptr = lk.Pointer
			continue
		}

		s.writeHit(w, r, b, lk.Pointer, res)
		return
	}
}

// writeBudgetExhausted writes the response for a supersede loop that did
// not resolve within supersedeBudget.
func (s *Server) writeBudgetExhausted(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "1")
	w.WriteHeader(http.StatusServiceUnavailable)
}

// internalError writes a 500 with an empty body and logs the error.
func (s *Server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.Error("internal error", "topic", r.PathValue("topic"), "error", err)
	w.WriteHeader(http.StatusInternalServerError)
}
