package ingest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/sonirico/rpkv/clock/clocktest"
)

const testRefreshInterval = time.Second

// fakeSyncer counts SyncPartitions calls and returns a fixed error, if
// any, on every call.
type fakeSyncer struct {
	calls atomic.Int64
	err   error
}

func (f *fakeSyncer) SyncPartitions(context.Context) error {
	f.calls.Add(1)
	return f.err
}

// newTestRefresher wires a fresh mock clock and syncer into a Refresher.
func newTestRefresher(t *testing.T, syncer partitionSyncer) (*Refresher, *clocktest.Mock) {
	t.Helper()

	clk := clocktest.NewMock(time.Unix(0, 0))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	return NewRefresher(syncer, clk, testRefreshInterval, logger), clk
}

func TestRefresher(t *testing.T) {
	type testCase struct {
		name      string
		syncerErr error
		ticks     int
		wantCalls int64
	}

	tests := []testCase{
		{
			name:      "calls SyncPartitions once per tick",
			ticks:     3,
			wantCalls: 3,
		},
		{
			name:      "keeps ticking after a SyncPartitions error",
			syncerErr: errors.New("sync partitions: boom"),
			ticks:     3,
			wantCalls: 3,
		},
		{
			name:      "returns when the context is cancelled",
			ticks:     0,
			wantCalls: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			const notifyTimeout = 5 * time.Second

			syncer := &fakeSyncer{err: tc.syncerErr}
			r, clk := newTestRefresher(t, syncer)

			notify := make(chan struct{})
			clk.AfterNotify(notify)

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				r.RunLoop(ctx)
				close(done)
			}()

			for i := 0; i < tc.ticks; i++ {
				select {
				case <-notify:
				case <-time.After(notifyTimeout):
					t.Fatal("timed out waiting for notify")
				}
				clk.Advance(testRefreshInterval)
			}

			select {
			case <-notify:
			case <-time.After(notifyTimeout):
				t.Fatal("timed out waiting for notify")
			}
			cancel()

			select {
			case <-done:
			case <-time.After(notifyTimeout):
				t.Fatal("timed out waiting for RunLoop to return")
			}

			assert.Equal(t, tc.wantCalls, syncer.calls.Load())
		})
	}
}
