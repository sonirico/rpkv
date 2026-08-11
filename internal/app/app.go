// Package app is the wiring factory assembling index, ingest, fetch and
// server into a runnable process; the one place allowed to build
// dependencies.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/sonirico/rpkv/clock"
	"github.com/sonirico/rpkv/fetch"
	"github.com/sonirico/rpkv/index"
	"github.com/sonirico/rpkv/ingest"
	"github.com/sonirico/rpkv/internal/config"
	"github.com/sonirico/rpkv/internal/offsets"
	"github.com/sonirico/rpkv/server"
)

// topicRuntime bundles one topic's wired components.
type topicRuntime struct {
	db           *pebble.DB
	index        *index.Index
	ingestClient *kgo.Client
	fetchClient  *kgo.Client
	ingester     *ingest.Ingester
}

// App is the wired rpkv process: per-topic pipelines plus the HTTP server.
type App struct {
	cfg       config.Config
	logger    *slog.Logger
	server    *server.Server
	topics    []*topicRuntime
	addrCh    chan string
	closeOnce sync.Once
}

// New opens each topic's index, wires its ingest and fetch clients and
// assembles the HTTP server, per cfg.
func New(cfg config.Config, logger *slog.Logger, clk clock.Clock) (*App, error) {
	var topics []*topicRuntime
	backends := make(map[string]server.Backend, len(cfg.Topics))

	for _, topic := range cfg.Topics {
		db, err := pebble.Open(filepath.Join(cfg.DataDir, "topics", topic), &pebble.Options{})
		if err != nil {
			if closeErr := closeTopicRuntimes(topics); closeErr != nil {
				logger.Error("close partial app", "error", closeErr)
			}
			return nil, fmt.Errorf("app: open pebble %q: %w", topic, err)
		}

		ix := index.New(db)

		ingestClient, err := kgo.NewClient(kgo.SeedBrokers(cfg.Brokers...))
		if err != nil {
			if closeErr := ix.Close(); closeErr != nil {
				logger.Error("close partial app", "error", closeErr)
			}
			if closeErr := closeTopicRuntimes(topics); closeErr != nil {
				logger.Error("close partial app", "error", closeErr)
			}
			return nil, fmt.Errorf("app: ingest client %q: %w", topic, err)
		}

		fetchClient, err := kgo.NewClient(kgo.SeedBrokers(cfg.Brokers...))
		if err != nil {
			ingestClient.Close()
			if closeErr := ix.Close(); closeErr != nil {
				logger.Error("close partial app", "error", closeErr)
			}
			if closeErr := closeTopicRuntimes(topics); closeErr != nil {
				logger.Error("close partial app", "error", closeErr)
			}
			return nil, fmt.Errorf("app: fetch client %q: %w", topic, err)
		}

		ing := ingest.New(ingestClient, ix, topic, logger)
		f := fetch.New(fetchClient, topic)
		src := offsets.NewSource(kadm.NewClient(fetchClient), topic)

		topics = append(topics, &topicRuntime{
			db:           db,
			index:        ix,
			ingestClient: ingestClient,
			fetchClient:  fetchClient,
			ingester:     ing,
		})

		backends[topic] = server.NewBackend(ix, f, src)
	}

	srv := server.New(backends, clk, logger)

	return &App{
		cfg:    cfg,
		logger: logger,
		server: srv,
		topics: topics,
		addrCh: make(chan string, 1),
	}, nil
}

// Run listens on cfg.Listen, starts one ingester goroutine per topic plus
// the HTTP server, and blocks until ctx is cancelled or a component fails.
func (a *App) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", a.cfg.Listen)
	if err != nil {
		return fmt.Errorf("app: listen %s: %w", a.cfg.Listen, err)
	}
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	a.addrCh <- ln.Addr().String()

	httpServer := &http.Server{Handler: a.server}

	errCh := make(chan error, len(a.topics)+1)
	for _, rt := range a.topics {
		go func() {
			errCh <- rt.ingester.Run(runCtx)
		}()
	}
	go func() {
		errCh <- httpServer.Serve(ln)
	}()

	var firstErr error
	received := 0
	select {
	case <-ctx.Done():
		firstErr = ctx.Err()
	case runErr := <-errCh:
		received++
		if runErr != nil && !errors.Is(runErr, http.ErrServerClosed) &&
			!errors.Is(runErr, context.Canceled) {
			firstErr = runErr
		}
	}

	cancelRun()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		a.logger.Error("shutdown http server", "error", err)
	}
	cancel()

	total := len(a.topics) + 1
	for received < total {
		runErr := <-errCh
		received++
		if runErr != nil && !errors.Is(runErr, http.ErrServerClosed) &&
			!errors.Is(runErr, context.Canceled) {
			if firstErr == nil || errors.Is(firstErr, context.Canceled) ||
				errors.Is(firstErr, context.DeadlineExceeded) {
				firstErr = runErr
			}
		}
	}

	if errors.Is(firstErr, context.Canceled) {
		return nil
	}
	return firstErr
}

// Addr blocks until Run has bound its listener, then returns its address.
func (a *App) Addr(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case addr := <-a.addrCh:
		select {
		case a.addrCh <- addr:
		default:
		}
		return addr, nil
	}
}

// Close releases every topic's clients and index. Idempotent.
func (a *App) Close() error {
	var err error
	a.closeOnce.Do(func() {
		err = closeTopicRuntimes(a.topics)
	})
	return err
}

// closeTopicRuntimes closes every already-opened topicRuntime, joining any
// index close errors. kgo client Close returns nothing.
func closeTopicRuntimes(topics []*topicRuntime) error {
	var errs []error
	for _, rt := range topics {
		rt.ingestClient.Close()
		rt.fetchClient.Close()
		if err := rt.index.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
