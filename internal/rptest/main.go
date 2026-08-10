package rptest

import (
	"io"
	"log"
	"os"
	"testing"

	"github.com/ory/dockertest/v3"
	"github.com/sonirico/vago/testit"
	"github.com/sonirico/vago/testit/redpanda"
)

// clusterConfig is the single place fixing the broker configuration every
// integration test and benchmark runs against. Keys are applied with rpk
// once the broker answers, before any test runs.
// log_compaction_interval_ms keeps compaction passes frequent enough for
// fetch's superseded/evicted tests to observe them within their timeouts.
var clusterConfig = map[string]string{
	"log_compaction_interval_ms": "500",
}

// Main is the TestMain body for integration test binaries: it provisions
// one Redpanda broker via testit/redpanda, runs the tests against it and
// tears it down. When RPKV_TEST_BROKERS is set no container is started
// and tests run against that address instead - which must already carry
// clusterConfig.
func Main(m *testing.M) {
	if os.Getenv(brokersEnvVar) != "" {
		os.Exit(m.Run())
	}

	logger := newWriterLogger(os.Stderr)

	res, err := redpanda.NewResource(
		"",
		redpanda.WithLogger(logger),
		redpanda.WithClusterConfig(clusterConfig),
		redpanda.WithSetEnvFunc(func(dockerhost string, resource *dockertest.Resource) {
			brokerAddr = redpanda.BrokerAddr(dockerhost, resource)
		}),
	)
	if err != nil {
		logger.Errorf("rptest: build redpanda resource: %s", err)
		os.Exit(1)
	}

	pool := testit.NewDockerResourcesPool(logger, "", res)
	if err := pool.Up(); err != nil {
		logger.Errorf("rptest: start redpanda: %s", err)
		os.Exit(1)
	}

	testit.RunSafe(m, logger, pool)
}

// writerLogger satisfies testit.Logger by writing lifecycle logs to w -
// stderr in Main, where `go test` surfaces provisioning failures. It
// leans on stdlib log.Logger so write failures need no handling here.
type writerLogger struct {
	logger *log.Logger
}

func newWriterLogger(w io.Writer) testit.Logger {
	return writerLogger{logger: log.New(w, "", 0)}
}

func (l writerLogger) Info(args ...any) {
	l.logger.Println(args...)
}

func (l writerLogger) Infof(format string, args ...any) {
	l.logger.Printf(format, args...)
}

func (l writerLogger) Errorf(format string, args ...any) {
	l.logger.Printf(format, args...)
}
