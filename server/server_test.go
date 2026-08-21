package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonirico/rpkv/clock/clocktest"
	"github.com/sonirico/rpkv/fetch"
	"github.com/sonirico/rpkv/index"
	"github.com/sonirico/rpkv/metrics/metricstest"
)

type fakeIndex struct {
	lookups         []index.Lookup
	lookupErr       error
	lookupCalls     int
	checkpoints     []int64
	checkpointErr   error
	checkpointCalls int
}

func (f *fakeIndex) Get(key []byte) (index.Lookup, error) {
	if f.lookupErr != nil {
		return index.Lookup{}, f.lookupErr
	}
	lk := f.lookups[stickyIndex(f.lookupCalls, len(f.lookups))]
	f.lookupCalls++
	return lk, nil
}

func (f *fakeIndex) Checkpoint(partition int32) (int64, error) {
	if f.checkpointErr != nil {
		return 0, f.checkpointErr
	}
	cp := f.checkpoints[stickyIndex(f.checkpointCalls, len(f.checkpoints))]
	f.checkpointCalls++
	return cp, nil
}

type fakeFetcher struct {
	results []fetch.Result
	calls   int
	err     error
}

func (f *fakeFetcher) FetchAt(
	ctx context.Context,
	ptr index.Pointer,
	key []byte,
) (fetch.Result, error) {
	if f.err != nil {
		return fetch.Result{}, f.err
	}
	res := f.results[stickyIndex(f.calls, len(f.results))]
	f.calls++
	return res, nil
}

type fakeOffsets struct {
	ends map[int32]int64
	err  error
}

func (f *fakeOffsets) LogEndOffsets(ctx context.Context) (map[int32]int64, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.ends, nil
}

type fakeShapeSource struct {
	shape TopicShape
}

func (f *fakeShapeSource) Shape() TopicShape {
	return f.shape
}

func newTestShapeSource(shape TopicShape) *fakeShapeSource {
	return &fakeShapeSource{shape: shape}
}

// stickyIndex returns the index into a slice of length n for the calls-th
// call: calls itself while within bounds, the last valid index once
// exhausted.
func stickyIndex(calls, n int) int {
	if calls >= n {
		return n - 1
	}
	return calls
}

func newTestServer(
	t *testing.T,
	backends map[string]Backend,
	opts ...Option,
) (*Server, *clocktest.Mock) {
	t.Helper()

	clk := clocktest.NewMock(time.Unix(0, 0))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(backends, clk, logger, opts...), clk
}

func newTestBackend(ix indexReader, f valueFetcher, o offsetSource, s shapeSource) Backend {
	return NewBackend(ix, f, o, s)
}

func TestServerGet(t *testing.T) {
	errTransport := errors.New("transport error")

	type testCase struct {
		name        string
		backends    map[string]Backend
		url         string
		wantStatus  int
		wantBody    string
		wantHeaders map[string]string
	}

	hitTimestamp := time.UnixMilli(1700000000000).UTC()
	hitPtr := index.Pointer{Partition: 1, Offset: 7}
	hitBackends := map[string]Backend{
		"orders": newTestBackend(
			&fakeIndex{
				lookups:     []index.Lookup{{Pointer: hitPtr, Found: true}},
				checkpoints: []int64{9},
			},
			&fakeFetcher{results: []fetch.Result{{Value: []byte("v1"), Timestamp: hitTimestamp}}},
			&fakeOffsets{},
			newTestShapeSource(TopicShape{}),
		),
	}

	tests := []testCase{
		{
			name:       "hit",
			backends:   hitBackends,
			url:        "/v1/kv/orders/k1",
			wantStatus: http.StatusOK,
			wantBody:   "v1",
			wantHeaders: map[string]string{
				"X-Rpkv-Partition":  "1",
				"X-Rpkv-Offset":     "7",
				"X-Rpkv-Checkpoint": "9",
				"X-Rpkv-Timestamp":  "1700000000000",
			},
		},
		{
			name: "key not in index",
			backends: map[string]Backend{
				"orders": newTestBackend(
					&fakeIndex{lookups: []index.Lookup{{Found: false}}},
					&fakeFetcher{},
					&fakeOffsets{},
					newTestShapeSource(TopicShape{}),
				),
			},
			url:        "/v1/kv/orders/missing",
			wantStatus: http.StatusNotFound,
			wantBody:   "",
		},
		{
			name: "evicted",
			backends: map[string]Backend{
				"orders": newTestBackend(
					&fakeIndex{lookups: []index.Lookup{{Pointer: hitPtr, Found: true}}},
					&fakeFetcher{results: []fetch.Result{{Evicted: true}}},
					&fakeOffsets{},
					newTestShapeSource(TopicShape{}),
				),
			},
			url:        "/v1/kv/orders/k1",
			wantStatus: http.StatusGone,
			wantBody:   "",
		},
		{
			name:       "topic not indexed",
			backends:   hitBackends,
			url:        "/v1/kv/missing/k1",
			wantStatus: http.StatusNotFound,
			wantBody:   "topic not indexed",
		},
		{
			name:     "base64url key",
			backends: hitBackends,
			url: "/v1/kv/orders/" + base64.RawURLEncoding.EncodeToString(
				[]byte("k1"),
			) + "?key_encoding=base64url",
			wantStatus: http.StatusOK,
			wantBody:   "v1",
			wantHeaders: map[string]string{
				"X-Rpkv-Partition":  "1",
				"X-Rpkv-Offset":     "7",
				"X-Rpkv-Checkpoint": "9",
				"X-Rpkv-Timestamp":  "1700000000000",
			},
		},
		{
			name:       "invalid base64url",
			backends:   hitBackends,
			url:        "/v1/kv/orders/!!!?key_encoding=base64url",
			wantStatus: http.StatusBadRequest,
			wantBody:   "",
		},
		{
			name:       "unknown key_encoding",
			backends:   hitBackends,
			url:        "/v1/kv/orders/k1?key_encoding=hex",
			wantStatus: http.StatusBadRequest,
			wantBody:   "",
		},
		{
			name: "index Get error",
			backends: map[string]Backend{
				"orders": newTestBackend(
					&fakeIndex{lookupErr: errTransport},
					&fakeFetcher{},
					&fakeOffsets{},
					newTestShapeSource(TopicShape{}),
				),
			},
			url:        "/v1/kv/orders/k1",
			wantStatus: http.StatusInternalServerError,
			wantBody:   "",
		},
		{
			name: "fetch transport error",
			backends: map[string]Backend{
				"orders": newTestBackend(
					&fakeIndex{lookups: []index.Lookup{{Pointer: hitPtr, Found: true}}},
					&fakeFetcher{err: errTransport},
					&fakeOffsets{},
					newTestShapeSource(TopicShape{}),
				),
			},
			url:        "/v1/kv/orders/k1",
			wantStatus: http.StatusInternalServerError,
			wantBody:   "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newTestServer(t, tc.backends)
			req := httptest.NewRequest(http.MethodGet, tc.url, nil)
			rec := httptest.NewRecorder()

			srv.ServeHTTP(rec, req)

			assert.Equal(t, tc.wantStatus, rec.Code)
			assert.Equal(t, tc.wantBody, rec.Body.String())
			for header, want := range tc.wantHeaders {
				assert.Equal(t, want, rec.Header().Get(header))
			}
		})
	}

	t.Run("hit observes one request duration sample", func(t *testing.T) {
		requestDuration := metricstest.NewHistogram()
		srv, _ := newTestServer(
			t,
			hitBackends,
			WithMetrics(Metrics{RequestDuration: requestDuration}),
		)
		req := httptest.NewRequest(http.MethodGet, "/v1/kv/orders/k1", nil)
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		assert.Len(t, requestDuration.Observations(), 1)
	})
}

func TestServerGetSupersede(t *testing.T) {
	const notifyTimeout = 5 * time.Second

	t.Run("resolves within budget", func(t *testing.T) {
		supersedeTimestamp := time.UnixMilli(1700000000000).UTC()
		ptrA := index.Pointer{Partition: 0, Offset: 10}
		ptrB := index.Pointer{Partition: 0, Offset: 30}
		backends := map[string]Backend{
			"orders": newTestBackend(
				&fakeIndex{
					lookups: []index.Lookup{
						{Pointer: ptrA, Found: true},
						{Pointer: ptrB, Found: true},
					},
					checkpoints: []int64{5, 40},
				},
				&fakeFetcher{results: []fetch.Result{
					{Superseded: true},
					{Value: []byte("v2"), Timestamp: supersedeTimestamp},
				}},
				&fakeOffsets{},
				newTestShapeSource(TopicShape{}),
			),
		}
		supersedeRetries := metricstest.NewCounter()
		srv, clk := newTestServer(
			t,
			backends,
			WithMetrics(Metrics{SupersedeRetries: supersedeRetries}),
		)
		notify := make(chan struct{})
		clk.AfterNotify(notify)

		req := httptest.NewRequest(http.MethodGet, "/v1/kv/orders/k1", nil)
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			done <- rec
		}()

		for i := 0; i < 2; i++ {
			select {
			case <-notify:
			case <-time.After(notifyTimeout):
				t.Fatal("timed out waiting for notify")
			}
			clk.Advance(checkpointPollInterval)
		}

		var rec *httptest.ResponseRecorder
		select {
		case rec = <-done:
		case <-time.After(notifyTimeout):
			t.Fatal("timed out waiting for handler to finish")
		}

		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "v2", rec.Body.String())
		assert.Equal(t, "0", rec.Header().Get("X-Rpkv-Partition"))
		assert.Equal(t, "30", rec.Header().Get("X-Rpkv-Offset"))
		assert.Equal(t, "40", rec.Header().Get("X-Rpkv-Checkpoint"))
		assert.Equal(t, "1700000000000", rec.Header().Get("X-Rpkv-Timestamp"))
		assert.Equal(t, float64(2), supersedeRetries.Count())
	})

	t.Run("budget exhausted", func(t *testing.T) {
		ptrA := index.Pointer{Partition: 0, Offset: 10}
		backends := map[string]Backend{
			"orders": newTestBackend(
				&fakeIndex{
					lookups:     []index.Lookup{{Pointer: ptrA, Found: true}},
					checkpoints: []int64{5},
				},
				&fakeFetcher{results: []fetch.Result{{Superseded: true}}},
				&fakeOffsets{},
				newTestShapeSource(TopicShape{}),
			),
		}
		supersedeRetries := metricstest.NewCounter()
		srv, clk := newTestServer(
			t,
			backends,
			WithMetrics(Metrics{SupersedeRetries: supersedeRetries}),
		)
		notify := make(chan struct{})
		clk.AfterNotify(notify)

		req := httptest.NewRequest(http.MethodGet, "/v1/kv/orders/k1", nil)
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			done <- rec
		}()

		var rec *httptest.ResponseRecorder
	drive:
		for i := 0; i < 41; i++ {
			select {
			case <-notify:
				clk.Advance(checkpointPollInterval)
			case rec = <-done:
				break drive
			case <-time.After(notifyTimeout):
				t.Fatal("timed out waiting for notify or done")
			}
		}
		if rec == nil {
			select {
			case rec = <-done:
			case <-time.After(notifyTimeout):
				t.Fatal("timed out waiting for handler to finish")
			}
		}

		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
		assert.Equal(t, "1", rec.Header().Get("Retry-After"))
		assert.Equal(t, "", rec.Body.String())
		assert.Equal(t, float64(40), supersedeRetries.Count())
	})

	t.Run("re-resolve finds tombstone", func(t *testing.T) {
		ptrA := index.Pointer{Partition: 0, Offset: 10}
		backends := map[string]Backend{
			"orders": newTestBackend(
				&fakeIndex{
					lookups: []index.Lookup{
						{Pointer: ptrA, Found: true},
						{Pointer: index.Pointer{}, Found: false},
					},
					checkpoints: []int64{40},
				},
				&fakeFetcher{results: []fetch.Result{{Superseded: true}}},
				&fakeOffsets{},
				newTestShapeSource(TopicShape{}),
			),
		}
		supersedeRetries := metricstest.NewCounter()
		srv, clk := newTestServer(
			t,
			backends,
			WithMetrics(Metrics{SupersedeRetries: supersedeRetries}),
		)
		notify := make(chan struct{})
		clk.AfterNotify(notify)

		req := httptest.NewRequest(http.MethodGet, "/v1/kv/orders/k1", nil)
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			done <- rec
		}()

		select {
		case <-notify:
		case <-time.After(notifyTimeout):
			t.Fatal("timed out waiting for notify")
		}
		clk.Advance(checkpointPollInterval)

		var rec *httptest.ResponseRecorder
		select {
		case rec = <-done:
		case <-time.After(notifyTimeout):
			t.Fatal("timed out waiting for handler to finish")
		}

		require.Equal(t, http.StatusNotFound, rec.Code)
		assert.Equal(t, "", rec.Body.String())
		assert.Equal(t, float64(1), supersedeRetries.Count())
	})
}

func TestServerHealthz(t *testing.T) {
	t.Run("reports checkpoints and lag", func(t *testing.T) {
		backends := map[string]Backend{
			"orders": newTestBackend(
				&fakeIndex{checkpoints: []int64{41}},
				&fakeFetcher{},
				&fakeOffsets{ends: map[int32]int64{0: 42}},
				newTestShapeSource(TopicShape{}),
			),
			"events": newTestBackend(
				&fakeIndex{checkpoints: []int64{-1}},
				&fakeFetcher{},
				&fakeOffsets{ends: map[int32]int64{0: 42}},
				newTestShapeSource(TopicShape{}),
			),
		}
		srv, _ := newTestServer(t, backends)
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

		var got healthResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))

		want := healthResponse{
			Topics: map[string]healthTopic{
				"orders": {
					Partitions: []healthPartition{
						{Partition: 0, Checkpoint: 41, LogEnd: 42, Lag: 0},
					},
				},
				"events": {
					Partitions: []healthPartition{
						{Partition: 0, Checkpoint: -1, LogEnd: 42, Lag: 42},
					},
				},
			},
		}
		assert.Equal(t, want, got)
	})

	t.Run("offset source failure degrades to error entry", func(t *testing.T) {
		backends := map[string]Backend{
			"orders": newTestBackend(
				&fakeIndex{},
				&fakeFetcher{},
				&fakeOffsets{err: errors.New("offsets unavailable")},
				newTestShapeSource(TopicShape{}),
			),
		}
		srv, _ := newTestServer(t, backends)
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)

		var got healthResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))

		topic, ok := got.Topics["orders"]
		require.True(t, ok)
		assert.NotEmpty(t, topic.Error)
		assert.Empty(t, topic.Partitions)
	})

	t.Run("reports partition count and cleanup policy from the shape source", func(t *testing.T) {
		backends := map[string]Backend{
			"orders": newTestBackend(
				&fakeIndex{checkpoints: []int64{41}},
				&fakeFetcher{},
				&fakeOffsets{ends: map[int32]int64{0: 42}},
				newTestShapeSource(TopicShape{PartitionCount: 3, CleanupPolicy: "compact"}),
			),
		}
		srv, _ := newTestServer(t, backends)
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)

		var got healthResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))

		topic, ok := got.Topics["orders"]
		require.True(t, ok)
		assert.Equal(t, int32(3), topic.PartitionCount)
		assert.Equal(t, "compact", topic.CleanupPolicy)
	})
}
