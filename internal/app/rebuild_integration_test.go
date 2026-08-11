//go:build integration

// This is the roadmap's rebuild-convergence proof: the index is a
// disposable projection over the log (ADR-001), so deleting it and
// replaying the same topic must reproduce the exact same visible state.
// Comparing full header-level snapshots (not just body bytes) pins the
// rebuilt (partition, offset, checkpoint) pointers to their pre-delete
// values, so an equal-but-empty pair of maps cannot pass and a rebuild
// that resolves keys correctly but drifts on pointer bookkeeping is
// still caught.
package app_test

import (
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/sonirico/rpkv/internal/config"
	"github.com/sonirico/rpkv/internal/rptest"
)

// kvSnapshot captures one key's full observable state at a point in time:
// HTTP status, the three rpkv pointer headers (empty string when absent),
// and the response body.
type kvSnapshot struct {
	Status     int
	Partition  string
	Offset     string
	Checkpoint string
	Body       []byte
}

// snapshotKeys GETs every key against topic and records its kvSnapshot,
// keyed by key.
func snapshotKeys(
	t *testing.T,
	client *http.Client,
	baseURL, topic string,
	keys []string,
) map[string]kvSnapshot {
	t.Helper()

	snapshot := make(map[string]kvSnapshot, len(keys))
	for _, key := range keys {
		resp, body := getKV(t, client, baseURL, topic, key)

		snapshot[key] = kvSnapshot{
			Status:     resp.StatusCode,
			Partition:  resp.Header.Get("X-Rpkv-Partition"),
			Offset:     resp.Header.Get("X-Rpkv-Offset"),
			Checkpoint: resp.Header.Get("X-Rpkv-Checkpoint"),
			Body:       body,
		}
	}

	return snapshot
}

func TestRebuildConvergence(t *testing.T) {
	adminClient, err := kgo.NewClient(kgo.SeedBrokers(rptest.Brokers()))
	require.NoError(t, err)
	t.Cleanup(adminClient.Close)
	admin := kadm.NewClient(adminClient)

	producer, err := kgo.NewClient(kgo.SeedBrokers(rptest.Brokers()))
	require.NoError(t, err)
	t.Cleanup(producer.Close)

	topic := newTestCompactionTopic(t, admin)

	dataDir := t.TempDir()
	cfg := config.Config{
		Brokers: []string{rptest.Brokers()},
		Topics:  []string{topic},
		DataDir: dataDir,
		Listen:  "127.0.0.1:0",
	}

	client := &http.Client{Timeout: 10 * time.Second}
	rng := rand.New(rand.NewSource(11))

	live := make(map[string][]byte)
	tombstoned := make(map[string]struct{})
	seq := 0

	produceCompactionRound(
		t, producer, topic, rng, live, tombstoned, &seq,
		func(s int) []byte { return []byte(fmt.Sprintf("rebuild-s%d", s)) },
		"fill-rebuild",
		"not enough live keys to tombstone",
	)

	baseURL, stop := startTestApp(t, cfg)
	t.Cleanup(func() { stop() })

	waitForQuiescence(t, client, baseURL, topic)

	allKeys := make([]string, 0, len(live)+len(tombstoned))
	for key := range live {
		allKeys = append(allKeys, key)
	}
	for key := range tombstoned {
		allKeys = append(allKeys, key)
	}
	sort.Strings(allKeys)

	snapA := snapshotKeys(t, client, baseURL, topic, allKeys)

	assertModel(t, client, baseURL, topic, live, tombstoned)

	stop()
	require.NoError(t, os.RemoveAll(dataDir))

	baseURL, stop = startTestApp(t, cfg)

	waitForQuiescence(t, client, baseURL, topic)

	snapB := snapshotKeys(t, client, baseURL, topic, allKeys)

	require.Equal(t, snapA, snapB, "rebuilt index state diverged from pre-delete state")
}
