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
	"github.com/sonirico/rpkv/internal/promsink"
	"github.com/sonirico/rpkv/internal/topicshape"
	"github.com/sonirico/rpkv/router"
	"github.com/sonirico/rpkv/server"
)

// topicRuntime bundles one topic's wired components.
type topicRuntime struct {
	db           *pebble.DB
	index        *index.Index
	ingestClient *kgo.Client
	fetchClient  *kgo.Client
	ingester     *ingest.Ingester
	refresher    *ingest.Refresher
	shapeWatcher *topicshape.Watcher
}

// App is the wired rpkv process: per-topic pipelines plus the HTTP server.
type App struct {
	cfg            config.Config
	logger         *slog.Logger
	handler        http.Handler
	metricsHandler http.Handler
	topics         []*topicRuntime
	addrCh         chan string
	closeOnce      sync.Once
}

// New opens each topic's index, wires its ingest and fetch clients and
// assembles the HTTP server, per cfg.
func New(cfg config.Config, logger *slog.Logger, clk clock.Clock) (*App, error) {
	var topics []*topicRuntime
	backends := make(map[string]server.Backend, len(cfg.Topics))

	sinks, err := promsink.New(logger)
	if err != nil {
		return nil, fmt.Errorf("app: metrics sinks: %w", err)
	}

	if cfg.Mode == config.ModeRouter {
		return &App{
			cfg:            cfg,
			logger:         logger,
			handler:        router.New(cfg.Shards, &http.Client{}, logger, router.WithMetrics(sinks.RouterMetrics())),
			metricsHandler: sinks.Handler(),
			addrCh:         make(chan string, 1),
		}, nil
	}

	owned := index.NewOwnership(cfg.Partitions)

	for _, topic := range cfg.Topics {
		db, err := pebble.Open(filepath.Join(cfg.DataDir, "topics", topic), &pebble.Options{})
		if err != nil {
			if closeErr := closeTopicRuntimes(topics); closeErr != nil {
				logger.Error("close partial app", "error", closeErr)
			}
			return nil, fmt.Errorf("app: open pebble %q: %w", topic, err)
		}

		ix := index.New(db)

		if err := ix.EnsureOwnership(owned); err != nil {
			if closeErr := ix.Close(); closeErr != nil {
				logger.Error("close partial app", "error", closeErr)
			}
			if closeErr := closeTopicRuntimes(topics); closeErr != nil {
				logger.Error("close partial app", "error", closeErr)
			}
			return nil, fmt.Errorf("app: ensure ownership %q: %w", topic, err)
		}

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

		ing := ingest.New(
			ingestClient,
			ix,
			topic,
			logger,
			ingest.WithMetrics(sinks.IngestMetrics(topic)),
			ingest.WithPartitions(owned),
		)
		f := fetch.New(fetchClient, topic, fetch.WithMetrics(sinks.FetchMetrics(topic)))
		admin := kadm.NewClient(fetchClient)
		src := offsets.NewSource(admin, topic, owned)
		shapeWatcher := topicshape.NewWatcher(admin, admin, topic, clk, cfg.MetadataRefresh, logger)

		if err := sinks.RegisterLag(topic, ix, src); err != nil {
			fetchClient.Close()
			ingestClient.Close()
			if closeErr := ix.Close(); closeErr != nil {
				logger.Error("close partial app", "error", closeErr)
			}
			if closeErr := closeTopicRuntimes(topics); closeErr != nil {
				logger.Error("close partial app", "error", closeErr)
			}
			return nil, fmt.Errorf("app: register lag collector %q: %w", topic, err)
		}

		if err := sinks.RegisterAssignedPartitions(topic, ing); err != nil {
			fetchClient.Close()
			ingestClient.Close()
			if closeErr := ix.Close(); closeErr != nil {
				logger.Error("close partial app", "error", closeErr)
			}
			if closeErr := closeTopicRuntimes(topics); closeErr != nil {
				logger.Error("close partial app", "error", closeErr)
			}
			return nil, fmt.Errorf("app: register assigned partitions collector %q: %w", topic, err)
		}

		if err := sinks.RegisterShape(topic, shapeWatcher); err != nil {
			fetchClient.Close()
			ingestClient.Close()
			if closeErr := ix.Close(); closeErr != nil {
				logger.Error("close partial app", "error", closeErr)
			}
			if closeErr := closeTopicRuntimes(topics); closeErr != nil {
				logger.Error("close partial app", "error", closeErr)
			}
			return nil, fmt.Errorf("app: register shape collector %q: %w", topic, err)
		}

		refresher := ingest.NewRefresher(ing, clk, cfg.MetadataRefresh, logger)

		topics = append(topics, &topicRuntime{
			db:           db,
			index:        ix,
			ingestClient: ingestClient,
			fetchClient:  fetchClient,
			ingester:     ing,
			refresher:    refresher,
			shapeWatcher: shapeWatcher,
		})

		backends[topic] = server.NewBackend(ix, f, src, shapeWatcher)
	}

	srv := server.New(backends, clk, logger, server.WithMetrics(sinks.ServerMetrics()))

	return &App{
		cfg:            cfg,
		logger:         logger,
		handler:        srv,
		metricsHandler: sinks.Handler(),
		topics:         topics,
		addrCh:         make(chan string, 1),
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

	mux := http.NewServeMux()
	mux.Handle("/", a.handler)
	mux.Handle("GET /metrics", a.metricsHandler)

	httpServer := &http.Server{Handler: mux}

	errCh := make(chan error, len(a.topics)+1)
	for _, rt := range a.topics {
		go func() {
			errCh <- rt.ingester.Run(runCtx)
		}()
	}
	go func() {
		errCh <- httpServer.Serve(ln)
	}()

	var refresherWG sync.WaitGroup
	for _, rt := range a.topics {
		refresherWG.Add(1)
		go func() {
			defer refresherWG.Done()
			rt.refresher.RunLoop(runCtx)
		}()

		refresherWG.Add(1)
		go func() {
			defer refresherWG.Done()
			rt.shapeWatcher.RunLoop(runCtx)
		}()
	}

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
	refresherWG.Wait()

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
