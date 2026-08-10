//go:build integration

package ingest_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/sonirico/rpkv/index"
	"github.com/sonirico/rpkv/ingest"
	"github.com/sonirico/rpkv/internal/rptest"
)

const (
	testTimeout        = 60 * time.Second
	testEventually     = 30 * time.Second
	testEventuallyTick = 100 * time.Millisecond
	testPartitions     = 3
)

// testIngestFixture wires the unique topic, admin client and on-disk
// Pebble index a crash-restart test needs. The Pebble DB is reopened by
// the test itself (via dbDir) to simulate the restart; the fixture only
// owns what survives the whole test.
type testIngestFixture struct {
	topic  string
	dbDir  string
	logger *slog.Logger
}

func newTestIngestFixture(t *testing.T) testIngestFixture {
	t.Helper()

	adminClient, err := kgo.NewClient(kgo.SeedBrokers(rptest.Brokers()))
	require.NoError(t, err)
	t.Cleanup(adminClient.Close)

	admin := kadm.NewClient(adminClient)
	topic := newTestIngestTopicName(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	createResp, err := admin.CreateTopics(ctx, testPartitions, 1, nil, topic)
	require.NoError(t, err)
	for _, r := range createResp {
		require.NoError(t, r.Err)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		deleteResp, err := admin.DeleteTopics(ctx, topic)
		require.NoError(t, err)
		for _, r := range deleteResp {
			require.NoError(t, r.Err)
		}
	})

	return testIngestFixture{
		topic:  topic,
		dbDir:  t.TempDir(),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func newTestIngestTopicName(t *testing.T) string {
	t.Helper()

	suffix := make([]byte, 8)
	_, err := rand.Read(suffix)
	require.NoError(t, err)

	return fmt.Sprintf("rpkv-ingest-%s", hex.EncodeToString(suffix))
}

// newTestProduceClient builds a fresh producing kgo client, closed via
// t.Cleanup.
func newTestProduceClient(t *testing.T) *kgo.Client {
	t.Helper()

	client, err := kgo.NewClient(kgo.SeedBrokers(rptest.Brokers()))
	require.NoError(t, err)
	t.Cleanup(client.Close)

	return client
}

// newTestIndex opens a Pebble DB at dir and wraps it in an index.Index.
// Close is the caller's responsibility - the restart step closes the
// first index explicitly before reopening on the same dir, and only the
// final, long-lived index gets a t.Cleanup, mirroring
// index/index_test.go's newTestCrashedIndex.
func newTestIndex(t *testing.T, dir string) *index.Index {
	t.Helper()

	db, err := pebble.Open(dir, &pebble.Options{})
	require.NoError(t, err)

	return index.NewIndex(db)
}

// producedRecord is what a produce call told us actually happened: the
// key it wrote (nil for null-key records, which the ingester skips), the
// partition and offset the broker assigned, and whether it was a
// tombstone. It is ground truth for the naive materialization - not a
// re-consume of the topic.
type producedRecord struct {
	key       []byte
	partition int32
	offset    int64
	tombstone bool
}

// produceWave produces records and returns one producedRecord per input,
// in the same order, using the offsets and partitions the broker actually
// assigned.
func produceWave(t *testing.T, client *kgo.Client, records []*kgo.Record) []producedRecord {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	results := client.ProduceSync(ctx, records...)

	produced := make([]producedRecord, 0, len(results))
	for _, r := range results {
		require.NoError(t, r.Err)
		produced = append(produced, producedRecord{
			key:       r.Record.Key,
			partition: r.Record.Partition,
			offset:    r.Record.Offset,
			tombstone: r.Record.Value == nil,
		})
	}
	return produced
}

// buildWaveRecords generates a wave of records cycling through keys, with
// every fifth keyed record a tombstone and nilKeyCount null-key records
// interspersed.
func buildWaveRecords(topic string, keys []string, count, nilKeyCount int) []*kgo.Record {
	records := make([]*kgo.Record, 0, count)
	nilKeysPlaced := 0

	for i := 0; i < count; i++ {
		if nilKeysPlaced < nilKeyCount && i%7 == 3 {
			records = append(records, &kgo.Record{
				Topic: topic,
				Key:   nil,
				Value: []byte(fmt.Sprintf("nilkey-%d", i)),
			})
			nilKeysPlaced++
			continue
		}

		key := keys[i%len(keys)]
		var value []byte
		if i%5 != 4 {
			value = []byte(fmt.Sprintf("%s-v%d", key, i))
		} // else: nil value, a tombstone

		records = append(records, &kgo.Record{Topic: topic, Key: []byte(key), Value: value})
	}

	for nilKeysPlaced < nilKeyCount {
		records = append(records, &kgo.Record{
			Topic: topic,
			Key:   nil,
			Value: []byte(fmt.Sprintf("nilkey-extra-%d", nilKeysPlaced)),
		})
		nilKeysPlaced++
	}

	return records
}

// lastOffsetsByPartition returns, for every partition that appears across
// produced, the highest offset seen - including null-key records, which
// still advance the checkpoint even though they carry no index entry.
func lastOffsetsByPartition(produced ...[]producedRecord) map[int32]int64 {
	last := make(map[int32]int64)
	for _, wave := range produced {
		for _, r := range wave {
			if cur, ok := last[r.partition]; !ok || r.offset > cur {
				last[r.partition] = r.offset
			}
		}
	}
	return last
}

// materializeNaive folds produced records into the final key -> Pointer
// state a correct ingester must converge to: for every key, only its
// highest-offset record within its partition matters (the default
// partitioner is key-consistent, so a key never moves partitions), last
// non-tombstone write wins, a tombstone deletes.
func materializeNaive(produced ...[]producedRecord) map[string]index.Lookup {
	type latest struct {
		offset    int64
		partition int32
		tombstone bool
	}

	byKey := make(map[string]latest)
	for _, wave := range produced {
		for _, r := range wave {
			if r.key == nil {
				continue
			}
			k := string(r.key)
			if cur, ok := byKey[k]; !ok || r.offset > cur.offset {
				byKey[k] = latest{offset: r.offset, partition: r.partition, tombstone: r.tombstone}
			}
		}
	}

	want := make(map[string]index.Lookup, len(byKey))
	for k, l := range byKey {
		if l.tombstone {
			want[k] = index.Lookup{Found: false}
			continue
		}
		want[k] = index.Lookup{
			Pointer: index.Pointer{Partition: l.partition, Offset: l.offset},
			Found:   true,
		}
	}
	return want
}

func TestIngesterCrashRestartConvergence(t *testing.T) {
	fx := newTestIngestFixture(t)

	keys := make([]string, 15)
	for i := range keys {
		keys[i] = fmt.Sprintf("key-%02d", i)
	}

	producer := newTestProduceClient(t)
	wave1Records := buildWaveRecords(fx.topic, keys, 60, 2)
	wave1 := produceWave(t, producer, wave1Records)

	ix1 := newTestIndex(t, fx.dbDir)
	consumeClient1 := newTestProduceClient(t)
	ingester1 := ingest.NewIngester(consumeClient1, ix1, fx.topic, fx.logger)

	ctx1, cancel1 := context.WithCancel(context.Background())
	runErr1 := make(chan error, 1)
	go func() {
		runErr1 <- ingester1.Run(ctx1)
	}()

	t.Run("checkpoints converge", func(t *testing.T) {
		require.Eventually(t, func() bool {
			for p := int32(0); p < testPartitions; p++ {
				checkpoint, err := ix1.Checkpoint(p)
				if err != nil {
					return false
				}
				if checkpoint > -1 {
					return true
				}
			}
			return false
		}, testEventually, testEventuallyTick, "no partition checkpoint advanced past -1")
	})

	cancel1()
	err := <-runErr1
	require.True(t, errors.Is(err, context.Canceled), "want context.Canceled, got %v", err)

	wave2Records := buildWaveRecords(fx.topic, keys, 40, 3)
	wave2 := produceWave(t, producer, wave2Records)

	require.NoError(t, ix1.Close())

	ix2 := newTestIndex(t, fx.dbDir)
	t.Cleanup(func() {
		assert.NoError(t, ix2.Close())
	})
	consumeClient2 := newTestProduceClient(t)
	ingester2 := ingest.NewIngester(consumeClient2, ix2, fx.topic, fx.logger)

	ctx2, cancel2 := context.WithCancel(context.Background())
	runErr2 := make(chan error, 1)
	go func() {
		runErr2 <- ingester2.Run(ctx2)
	}()

	t.Run("checkpoints converge after restart", func(t *testing.T) {
		wantLastOffsets := lastOffsetsByPartition(wave1, wave2)
		require.Eventually(t, func() bool {
			for p, wantOffset := range wantLastOffsets {
				got, err := ix2.Checkpoint(p)
				if err != nil || got != wantOffset {
					return false
				}
			}
			return true
		}, testEventually, testEventuallyTick, "partitions did not converge to their last produced offsets")
	})

	cancel2()
	err = <-runErr2
	require.True(t, errors.Is(err, context.Canceled), "want context.Canceled, got %v", err)

	t.Run("materialization matches naive model", func(t *testing.T) {
		want := materializeNaive(wave1, wave2)
		for key, wantLookup := range want {
			got, err := ix2.Get([]byte(key))
			require.NoError(t, err)
			assert.Equal(t, wantLookup, got, "key %q", key)
		}
	})

	t.Run("null keys are counted", func(t *testing.T) {
		totalSkipped := ingester1.SkippedNullKeys() + ingester2.SkippedNullKeys()
		assert.GreaterOrEqual(t, totalSkipped, int64(2))
	})

	t.Run("consumer group is cleaned up", func(t *testing.T) {
		groupListCtx, groupListCancel := context.WithTimeout(context.Background(), testTimeout)
		defer groupListCancel()

		adminClient, err := kgo.NewClient(kgo.SeedBrokers(rptest.Brokers()))
		require.NoError(t, err)
		t.Cleanup(adminClient.Close)

		groups, err := kadm.NewClient(adminClient).ListGroups(groupListCtx)
		require.NoError(t, err)
		assert.Empty(t, groups, "expected no broker-side consumer groups")
	})
}
