package router

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonirico/rpkv/metrics/metricstest"
)

type testRouterFixture struct {
	router  *Router
	counter *metricstest.Counter
}

// newTestRouter starts one httptest.Server per handler, treating a nil
// handler as a shard whose server is closed before use (a shard that is
// down), builds a Router over their addresses with an accumulating
// counter, and returns both.
func newTestRouter(t *testing.T, handlers ...http.HandlerFunc) testRouterFixture {
	t.Helper()

	shards := make([]string, len(handlers))
	for i, h := range handlers {
		if h == nil {
			srv := httptest.NewServer(http.NotFoundHandler())
			shards[i] = strings.TrimPrefix(srv.URL, "http://")
			srv.Close()
			continue
		}
		srv := httptest.NewServer(h)
		t.Cleanup(srv.Close)
		shards[i] = strings.TrimPrefix(srv.URL, "http://")
	}

	counter := metricstest.NewCounter()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := New(
		shards,
		http.DefaultClient,
		logger,
		WithMetrics(Metrics{AmbiguousKeys: counter}),
	)

	return testRouterFixture{router: router, counter: counter}
}

func handlerStatus(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}
}

func handlerOK(body string, headers map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}
}

func TestRouter(t *testing.T) {
	type testCase struct {
		name           string
		handlers       []http.HandlerFunc
		wantStatus     int
		wantBody       string
		wantHeaders    map[string]string
		wantAmbiguous  bool
		wantCounter    float64
		wantRetryAfter string
	}

	tests := []testCase{
		{
			name: "single shard 200 proxies body and headers",
			handlers: []http.HandlerFunc{
				handlerOK("value-a", map[string]string{
					"X-Rpkv-Partition":  "1",
					"X-Rpkv-Offset":     "10",
					"X-Rpkv-Checkpoint": "20",
					"X-Rpkv-Timestamp":  "100",
				}),
			},
			wantStatus: http.StatusOK,
			wantBody:   "value-a",
			wantHeaders: map[string]string{
				"X-Rpkv-Partition":  "1",
				"X-Rpkv-Offset":     "10",
				"X-Rpkv-Checkpoint": "20",
				"X-Rpkv-Timestamp":  "100",
			},
			wantAmbiguous: false,
			wantCounter:   0,
		},
		{
			name: "two shards 200 picks highest timestamp",
			handlers: []http.HandlerFunc{
				handlerOK("value-old", map[string]string{"X-Rpkv-Timestamp": "100"}),
				handlerOK("value-new", map[string]string{"X-Rpkv-Timestamp": "200"}),
			},
			wantStatus:    http.StatusOK,
			wantBody:      "value-new",
			wantAmbiguous: true,
			wantCounter:   1,
		},
		{
			name: "two shards 200 one missing timestamp header, the other wins",
			handlers: []http.HandlerFunc{
				handlerOK("value-missing", nil),
				handlerOK("value-timed", map[string]string{"X-Rpkv-Timestamp": "50"}),
			},
			wantStatus:    http.StatusOK,
			wantBody:      "value-timed",
			wantAmbiguous: true,
			wantCounter:   1,
		},
		{
			name: "all 404 returns 404",
			handlers: []http.HandlerFunc{
				handlerStatus(http.StatusNotFound),
				handlerStatus(http.StatusNotFound),
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "404 and 410 returns 410",
			handlers: []http.HandlerFunc{
				handlerStatus(http.StatusNotFound),
				handlerStatus(http.StatusGone),
			},
			wantStatus: http.StatusGone,
		},
		{
			name: "404 and 503 returns 503 with Retry-After",
			handlers: []http.HandlerFunc{
				handlerStatus(http.StatusNotFound),
				handlerStatus(http.StatusServiceUnavailable),
			},
			wantStatus:     http.StatusServiceUnavailable,
			wantRetryAfter: "1",
		},
		{
			name: "404 and 400 returns 400",
			handlers: []http.HandlerFunc{
				handlerStatus(http.StatusNotFound),
				handlerStatus(http.StatusBadRequest),
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "a down shard plus a 404 returns 502",
			handlers: []http.HandlerFunc{
				nil,
				handlerStatus(http.StatusNotFound),
			},
			wantStatus: http.StatusBadGateway,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fixture := newTestRouter(t, tc.handlers...)
			req := httptest.NewRequest(http.MethodGet, "/v1/kv/mytopic/mykey", nil)
			rec := httptest.NewRecorder()

			fixture.router.ServeHTTP(rec, req)

			assert.Equal(t, tc.wantStatus, rec.Code)
			if tc.wantBody != "" {
				assert.Equal(t, tc.wantBody, rec.Body.String())
			}
			for k, v := range tc.wantHeaders {
				assert.Equal(t, v, rec.Header().Get(k))
			}
			if tc.wantAmbiguous {
				assert.Equal(t, "true", rec.Header().Get("X-Rpkv-Ambiguous"))
			} else {
				assert.Empty(t, rec.Header().Get("X-Rpkv-Ambiguous"))
			}
			if tc.wantRetryAfter != "" {
				assert.Equal(t, tc.wantRetryAfter, rec.Header().Get("Retry-After"))
			}
			assert.Equal(t, tc.wantCounter, fixture.counter.Count())
		})
	}

	t.Run("healthz returns mode and shards", func(t *testing.T) {
		t.Parallel()

		fixture := newTestRouter(t, handlerStatus(http.StatusNotFound), handlerStatus(http.StatusNotFound))
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()

		fixture.router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		var got healthResponse
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
		assert.Equal(t, "router", got.Mode)
		assert.Equal(t, fixture.router.shards, got.Shards)
	})
}
