package router

import (
	"io"
	"net/http"
	"strconv"
)

// shardResult is one shard's answer to a fanned-out request.
type shardResult struct {
	shard  string
	status int
	header http.Header
	body   []byte
	err    error
}

// handleKV serves GET /v1/kv/{topic}/{key} by asking every shard and
// arbitrating their responses.
func (s *Router) handleKV(w http.ResponseWriter, r *http.Request) {
	results := s.fanOut(r)

	oks := make([]shardResult, 0, len(results))
	for _, shard := range s.shards {
		for _, res := range results {
			if res.shard == shard && res.status == http.StatusOK {
				oks = append(oks, res)
			}
		}
	}

	if len(oks) > 0 {
		s.writeWinner(w, oks)
		return
	}

	for _, res := range results {
		if res.status == http.StatusGone {
			w.WriteHeader(http.StatusGone)
			return
		}
	}

	for _, res := range results {
		if res.status == http.StatusServiceUnavailable {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
	}

	for _, res := range results {
		if res.status == http.StatusBadRequest {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}

	for _, res := range results {
		if res.err != nil || res.status != http.StatusNotFound {
			reason := "status " + strconv.Itoa(res.status)
			if res.err != nil {
				reason = res.err.Error()
			}
			s.logger.Error("shard request failed", "shard", res.shard, "reason", reason)
			w.WriteHeader(http.StatusBadGateway)
			return
		}
	}

	w.WriteHeader(http.StatusNotFound)
}

// fanOut sends r to every shard concurrently and waits for all responses.
func (s *Router) fanOut(r *http.Request) []shardResult {
	results := make(chan shardResult, len(s.shards))
	for _, shard := range s.shards {
		go s.fetchShard(r, shard, results)
	}

	out := make([]shardResult, 0, len(s.shards))
	for range s.shards {
		out = append(out, <-results)
	}
	return out
}

// fetchShard proxies r to a single shard and sends its result on results.
func (s *Router) fetchShard(r *http.Request, shard string, results chan<- shardResult) {
	req, err := http.NewRequestWithContext(
		r.Context(),
		http.MethodGet,
		"http://"+shard+r.URL.EscapedPath(),
		nil,
	)
	if err != nil {
		results <- shardResult{shard: shard, err: err}
		return
	}

	resp, err := s.client.Do(req)
	if err != nil {
		results <- shardResult{shard: shard, err: err}
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		results <- shardResult{shard: shard, err: err}
		return
	}

	results <- shardResult{
		shard:  shard,
		status: resp.StatusCode,
		header: resp.Header,
		body:   body,
	}
}

// winnerHeaders are copied from the winning shard's response, when
// present.
var winnerHeaders = []string{
	"X-Rpkv-Partition",
	"X-Rpkv-Offset",
	"X-Rpkv-Checkpoint",
	"X-Rpkv-Timestamp",
}

// writeWinner picks the highest-timestamp 200 among oks, flags ambiguity
// when more than one shard answered 200, and writes the winner's headers,
// status and body.
func (s *Router) writeWinner(w http.ResponseWriter, oks []shardResult) {
	winner := oks[0]
	winnerTS := shardTimestamp(winner)
	for _, res := range oks[1:] {
		ts := shardTimestamp(res)
		if ts > winnerTS {
			winner = res
			winnerTS = ts
		}
	}

	if len(oks) > 1 {
		w.Header().Set("X-Rpkv-Ambiguous", "true")
		s.metrics.AmbiguousKeys.Inc()
	}

	for _, h := range winnerHeaders {
		if v := winner.header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(winner.body); err != nil {
		s.logger.Error("write value", "error", err)
	}
}

// shardTimestamp parses a shard's X-Rpkv-Timestamp header, returning 0
// when the header is absent or invalid.
func shardTimestamp(res shardResult) int64 {
	ts, err := strconv.ParseInt(res.header.Get("X-Rpkv-Timestamp"), 10, 64)
	if err != nil {
		return 0
	}
	return ts
}
