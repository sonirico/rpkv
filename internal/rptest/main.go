package rptest

import (
	"fmt"
	"io"
	"log"
	"os"
	"testing"

	"github.com/ory/dockertest/v3"
	"github.com/sonirico/vago/ent"
	"github.com/sonirico/vago/testit"
	"github.com/sonirico/vago/testit/minio"
	"github.com/sonirico/vago/testit/redpanda"
)

// tieredEnvVar switches Main into tiered mode: a MinIO resource is
// provisioned alongside Redpanda and Redpanda is booted with tiered
// storage bootstrap properties pointing at it, so tests can force local
// segment eviction and read back from object storage.
const tieredEnvVar = "RPKV_TEST_TIERED"

// tieredBucket is the MinIO bucket tiered-mode Redpanda uploads segments
// to.
const tieredBucket = "rpkv-it"

// clusterConfig is the single place fixing the broker configuration every
// integration test and benchmark runs against. Keys are applied with rpk
// once the broker answers, before any test runs.
// log_compaction_interval_ms keeps compaction passes frequent enough for
// fetch's superseded/evicted tests to observe them within their timeouts.
var clusterConfig = map[string]string{
	"log_compaction_interval_ms": "500",
}

// tieredClusterConfig extends clusterConfig for tiered mode.
// retention_local_strict forces local retention to be enforced against
// each topic's retention.local.target.bytes/ms as a hard cap rather than
// only opportunistically under local disk pressure, which is what lets
// the tiered read latency benchmark force local segment eviction
// deterministically.
var tieredClusterConfig = mergeClusterConfig(clusterConfig, map[string]string{
	"retention_local_strict": "true",
})

func mergeClusterConfig(base map[string]string, extra map[string]string) map[string]string {
	merged := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range extra {
		merged[k] = v
	}
	return merged
}

// tieredBootstrapProperties are applied as --set redpanda.<key>=<value>
// flags at redpanda start, pointing cloud storage at the MinIO resource
// Main provisions ahead of Redpanda in tiered mode. minio.InternalEndpoint
// is minio:9000, the MinIO container's hostname on the shared testit
// docker network.
var tieredBootstrapProperties = map[string]string{
	"cloud_storage_enabled":                         "true",
	"cloud_storage_access_key":                      "minioadmin",
	"cloud_storage_secret_key":                      "minioadmin",
	"cloud_storage_region":                          "local",
	"cloud_storage_bucket":                          tieredBucket,
	"cloud_storage_api_endpoint":                    "minio",
	"cloud_storage_api_endpoint_port":               "9000",
	"cloud_storage_disable_tls":                     "true",
	"cloud_storage_segment_max_upload_interval_sec": "1",
	"cloud_storage_housekeeping_interval_ms":        "500",
}

// Main is the TestMain body for integration test binaries: it provisions
// one Redpanda broker via testit/redpanda, runs the tests against it and
// tears it down. When RPKV_TEST_BROKERS is set no container is started
// and tests run against that address instead - which must already carry
// clusterConfig. When RPKV_TEST_TIERED is set, a MinIO resource is
// provisioned first and Redpanda is booted with tiered storage enabled
// against it; AdminAddr() then resolves to the provisioned broker's admin
// API.
func Main(m *testing.M) {
	logger := newWriterLogger(os.Stderr)

	if err := loadDotenv(dotenvFile); err != nil {
		logger.Errorf("rptest: load %s: %v", dotenvFile, err)
		os.Exit(1)
	}

	if ent.Get(brokersEnvVar, "") != "" {
		os.Exit(m.Run())
	}

	tiered := ent.Get(tieredEnvVar, "") != ""

	resources, err := buildResources(logger, tiered)
	if err != nil {
		logger.Errorf("rptest: %s", err)
		os.Exit(1)
	}

	pool := testit.NewDockerResourcesPool(logger, "", resources...)
	if err := pool.Up(); err != nil {
		logger.Errorf("rptest: start resources: %s", err)
		os.Exit(1)
	}

	testit.RunSafe(m, logger, pool)
}

// buildResources builds the resource list Main hands to the pool, in start
// order: MinIO first when tiered is set, so it is up before Redpanda's
// tiered storage bootstrap properties try to reach it, then Redpanda.
func buildResources(logger testit.Logger, tiered bool) ([]*testit.Resource, error) {
	var resources []*testit.Resource

	if tiered {
		minioRes, err := minio.NewResource(
			"",
			minio.WithLogger(logger),
			minio.WithBuckets(tieredBucket),
		)
		if err != nil {
			return nil, fmt.Errorf("build minio resource: %w", err)
		}
		resources = append(resources, minioRes)
	}

	redpandaOpts := []redpanda.Opt{
		redpanda.WithLogger(logger),
	}
	if tiered {
		redpandaOpts = append(redpandaOpts,
			redpanda.WithClusterConfig(tieredClusterConfig),
			redpanda.WithBootstrapProperties(tieredBootstrapProperties),
			redpanda.WithSetEnvFunc(func(dockerhost string, resource *dockertest.Resource) {
				brokerAddr = redpanda.BrokerAddr(dockerhost, resource)
				adminAddr = redpanda.AdminAddr(dockerhost, resource)
			}),
		)
	} else {
		redpandaOpts = append(redpandaOpts,
			redpanda.WithClusterConfig(clusterConfig),
			redpanda.WithSetEnvFunc(func(dockerhost string, resource *dockertest.Resource) {
				brokerAddr = redpanda.BrokerAddr(dockerhost, resource)
			}),
		)
	}

	res, err := redpanda.NewResource("", redpandaOpts...)
	if err != nil {
		return nil, fmt.Errorf("build redpanda resource: %w", err)
	}
	resources = append(resources, res)

	return resources, nil
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
