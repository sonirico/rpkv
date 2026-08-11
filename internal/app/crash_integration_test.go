//go:build integration

// This proves ADR-003: index apply and checkpoint advance commit in one
// Pebble batch, so a kill -9 at any point must leave the index equal to a
// naive materialization of the log at the index's own checkpoints.
//
// It runs rpkv as a real subprocess and sends it a real SIGKILL: the
// in-process startTestApp cannot be kill -9'd, and only a real process kill
// exercises real Pebble WAL recovery. Post-mortem inspection reads rpkv's
// own Pebble directory directly, because the invariant is masked over HTTP
// once a restarted server has caught back up - by the time a GET succeeds
// again, recovery has already happened. The final convergence check, run
// after the crash loop with no more kills pending, stays black-box.
package app_test

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/sonirico/rpkv/index"
	"github.com/sonirico/rpkv/internal/config"
	"github.com/sonirico/rpkv/internal/rptest"
)

const (
	crashRounds            = 5
	crashSeed              = 13
	crashKillDelayMinMs    = 200
	crashKillDelayJitterMs = 800
)

// newTestRpkvBinary builds cmd/rpkv once into a temp dir and returns the
// path to the resulting binary.
func newTestRpkvBinary(t *testing.T) string {
	t.Helper()

	bin := filepath.Join(t.TempDir(), "rpkv")
	out, err := exec.Command("go", "build", "-o", bin, "github.com/sonirico/rpkv/cmd/rpkv").
		CombinedOutput()
	require.NoError(t, err, "go build: %s", out)

	return bin
}

// startRpkvProcess starts bin against topic/dataDir/listen as a real
// subprocess, wired to be killable with a real SIGKILL. It registers a
// t.Cleanup that kills and reaps the process if the test did not already do
// so.
func startRpkvProcess(t *testing.T, bin, topic, dataDir, listen string) *exec.Cmd {
	t.Helper()

	cmd := exec.Command(
		bin,
		"--brokers",
		rptest.Brokers(),
		"--topics",
		topic,
		"--data-dir",
		dataDir,
		"--listen",
		listen,
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())

	t.Cleanup(func() {
		if cmd.ProcessState != nil {
			return
		}
		if err := cmd.Process.Kill(); err != nil {
			t.Logf("cleanup: kill rpkv process: %v", err)
		}
		if err := cmd.Wait(); err != nil {
			t.Logf("cleanup: wait rpkv process: %v", err)
		}
	})

	return cmd
}

// killRpkvProcess sends SIGKILL to cmd's process and reaps it, requiring the
// exit to be reported as an error since a killed process cannot exit clean.
func killRpkvProcess(t *testing.T, cmd *exec.Cmd) {
	t.Helper()

	require.NoError(t, cmd.Process.Kill())
	require.Error(t, cmd.Wait(), "SIGKILL must not exit clean")
}

// naiveMaterializeAt consumes topic from the start with a fresh client and
// materializes key -> Pointer as of cutoffs, the index's own per-partition
// checkpoints: records past a partition's cutoff are excluded, null-key
// records are skipped, and a nil value tombstones the key.
func naiveMaterializeAt(
	t *testing.T,
	topic string,
	cutoffs map[int32]int64,
) map[string]index.Pointer {
	t.Helper()

	client, err := kgo.NewClient(
		kgo.SeedBrokers(rptest.Brokers()),
		kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{
			topic: {
				0: kgo.NewOffset().At(0),
				1: kgo.NewOffset().At(0),
			},
		}),
	)
	require.NoError(t, err)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	maxSeen := make(map[int32]int64, len(cutoffs))
	for p := range cutoffs {
		maxSeen[p] = -1
	}
	records := make(map[int32][]*kgo.Record)

	caughtUp := func() bool {
		for p, cutoff := range cutoffs {
			if cutoff >= 0 && maxSeen[p] < cutoff {
				return false
			}
		}
		return true
	}

	for !caughtUp() {
		fetches := client.PollFetches(ctx)
		if ctx.Err() != nil {
			behind := make([]int32, 0)
			for p, cutoff := range cutoffs {
				if cutoff >= 0 && maxSeen[p] < cutoff {
					behind = append(behind, p)
				}
			}
			require.Fail(
				t,
				"naiveMaterializeAt: timed out waiting for cutoffs",
				"partitions still behind: %v",
				behind,
			)
		}
		fetches.EachError(func(_ string, _ int32, err error) {
			require.NoError(t, err)
		})

		fetches.EachRecord(func(r *kgo.Record) {
			records[r.Partition] = append(records[r.Partition], r)
			if r.Offset > maxSeen[r.Partition] {
				maxSeen[r.Partition] = r.Offset
			}
		})
	}

	m := make(map[string]index.Pointer)
	for p, cutoff := range cutoffs {
		for _, r := range records[p] {
			if r.Offset > cutoff {
				continue
			}
			if r.Key == nil {
				continue
			}
			if r.Value == nil {
				delete(m, string(r.Key))
				continue
			}
			m[string(r.Key)] = index.Pointer{Partition: r.Partition, Offset: r.Offset}
		}
	}

	return m
}

// assertCrashInvariants opens rpkv's own Pebble directory under dataDir
// directly and checks the two ADR-003 invariants that hold at any point,
// including mid-crash: every partition's persisted checkpoint is strictly
// behind the log's end offset (so the crash did not lose the ability to
// resume), and every key in allKeys resolves in the index exactly as a
// naive materialization of the log at those checkpoints would.
func assertCrashInvariants(
	t *testing.T,
	admin *kadm.Client,
	dataDir, topic string,
	allKeys []string,
) {
	t.Helper()

	db, err := pebble.Open(filepath.Join(dataDir, "topics", topic), &pebble.Options{})
	require.NoError(t, err)
	ix := index.New(db)

	cutoffs := make(map[int32]int64, 2)
	for _, p := range []int32{0, 1} {
		cp, err := ix.Checkpoint(p)
		require.NoError(t, err)
		cutoffs[p] = cp
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	endOffsets, err := admin.ListEndOffsets(ctx, topic)
	require.NoError(t, err)

	for p, cp := range cutoffs {
		if cp < 0 {
			continue
		}
		end, ok := endOffsets.Lookup(topic, p)
		require.True(t, ok, "partition %d", p)
		require.Less(t, cp, end.Offset, "partition %d", p)
	}

	naive := naiveMaterializeAt(t, topic, cutoffs)

	for _, key := range allKeys {
		lk, err := ix.Get([]byte(key))
		require.NoError(t, err)

		expected, present := naive[key]
		require.Equal(t, present, lk.Found, "key %q", key)
		if present {
			require.Equal(t, expected, lk.Pointer, "key %q", key)
		}
	}

	require.NoError(t, ix.Close())
}

func TestCrashConsistencySweep(t *testing.T) {
	adminClient, err := kgo.NewClient(kgo.SeedBrokers(rptest.Brokers()))
	require.NoError(t, err)
	t.Cleanup(adminClient.Close)
	admin := kadm.NewClient(adminClient)

	producer, err := kgo.NewClient(kgo.SeedBrokers(rptest.Brokers()))
	require.NoError(t, err)
	t.Cleanup(producer.Close)

	topic := newTestTopicName(t, "crash-")

	createCtx, createCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer createCancel()
	createResp, err := admin.CreateTopics(createCtx, 2, 1, nil, topic)
	require.NoError(t, err)
	for _, r := range createResp {
		require.NoError(t, r.Err)
	}
	t.Cleanup(func() {
		deleteCtx, deleteCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer deleteCancel()

		deleteResp, err := admin.DeleteTopics(deleteCtx, topic)
		require.NoError(t, err)
		for _, r := range deleteResp {
			require.NoError(t, r.Err)
		}
	})

	bin := newTestRpkvBinary(t)
	dataDir := t.TempDir()
	rng := rand.New(rand.NewSource(crashSeed))

	live := make(map[string][]byte)
	tombstoned := make(map[string]struct{})
	seq := 0

	for round := 0; round < crashRounds; round++ {
		produceCompactionRound(
			t, producer, topic, rng, live, tombstoned, &seq,
			func(s int) []byte { return []byte(fmt.Sprintf("crash-s%d", s)) },
			fmt.Sprintf("fill-crash-r%d", round),
			"not enough live keys to tombstone",
		)

		cmd := startRpkvProcess(t, bin, topic, dataDir, "127.0.0.1:0")

		// Fault injection choosing the randomized kill point, not test
		// synchronization: no assertion below depends on this delay.
		delay := time.Duration(
			crashKillDelayMinMs+rng.Intn(crashKillDelayJitterMs),
		) * time.Millisecond
		time.Sleep(delay)

		killRpkvProcess(t, cmd)

		allKeys := make([]string, 0, len(live)+len(tombstoned))
		for key := range live {
			allKeys = append(allKeys, key)
		}
		for key := range tombstoned {
			allKeys = append(allKeys, key)
		}
		sort.Strings(allKeys)

		assertCrashInvariants(t, admin, dataDir, topic, allKeys)
	}

	t.Run("final convergence", func(t *testing.T) {
		cfg := config.Config{
			Brokers: []string{rptest.Brokers()},
			Topics:  []string{topic},
			DataDir: dataDir,
			Listen:  "127.0.0.1:0",
		}
		baseURL, stop := startTestApp(t, cfg)
		t.Cleanup(func() { stop() })

		client := &http.Client{Timeout: 10 * time.Second}
		waitForQuiescence(t, client, baseURL, topic)
		assertModel(t, client, baseURL, topic, live, tombstoned)
	})
}
