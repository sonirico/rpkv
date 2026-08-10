//go:build integration

package fetch_test

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

	"github.com/sonirico/rpkv/fetch"
	"github.com/sonirico/rpkv/index"
	"github.com/sonirico/rpkv/ingest"
	"github.com/sonirico/rpkv/internal/rptest"
)

const (
	testTimeout            = 60 * time.Second
	testEventually         = 30 * time.Second
	testEventuallyTick     = 100 * time.Millisecond
	testEventuallyLong     = 60 * time.Second
	testEventuallyLongTick = 500 * time.Millisecond
	testFetchTimeout       = 5 * time.Second
	testPartitions         = 3

	fillerRecordCount = 200
	fillerRecordBytes = 64
	fillerChunkSize   = 10
)

// testFetchFixture wires the unique topic, on-disk Pebble index, a running
// Ingester feeding it, and a Fetcher on its own dedicated client, per
// fetch.NewFetcher's requirement that its client never be shared with an
// Ingester.
type testFetchFixture struct {
	topic    string
	index    *index.Index
	ingester *ingest.Ingester
	fetcher  *fetch.Fetcher
	producer *kgo.Client
}

func newTestFetchFixture(
	t *testing.T,
	partitions int32,
	configs map[string]*string,
) testFetchFixture {
	t.Helper()

	adminClient, err := kgo.NewClient(kgo.SeedBrokers(rptest.Brokers()))
	require.NoError(t, err)
	t.Cleanup(adminClient.Close)

	admin := kadm.NewClient(adminClient)
	topic := newTestFetchTopicName(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	createResp, err := admin.CreateTopics(ctx, partitions, 1, configs, topic)
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

	db, err := pebble.Open(t.TempDir(), &pebble.Options{})
	require.NoError(t, err)
	ix := index.NewIndex(db)
	t.Cleanup(func() {
		assert.NoError(t, ix.Close())
	})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ingestClient := newTestFetchClient(t)
	ingester := ingest.NewIngester(ingestClient, ix, topic, logger)

	runCtx, runCancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() {
		runErr <- ingester.Run(runCtx)
	}()
	t.Cleanup(func() {
		runCancel()
		err := <-runErr
		assert.True(t, errors.Is(err, context.Canceled), "want context.Canceled, got %v", err)
	})

	fetchClient := newTestFetchClient(t)
	fetcher := fetch.NewFetcher(fetchClient, topic)

	producer := newTestFetchClient(t)

	return testFetchFixture{
		topic:    topic,
		index:    ix,
		ingester: ingester,
		fetcher:  fetcher,
		producer: producer,
	}
}

func newTestFetchTopicName(t *testing.T) string {
	t.Helper()

	suffix := make([]byte, 8)
	_, err := rand.Read(suffix)
	require.NoError(t, err)

	return fmt.Sprintf("rpkv-fetch-%s", hex.EncodeToString(suffix))
}

// newTestFetchClient builds a fresh kgo client dedicated to a single role
// (producer, ingester consumer, or fetcher), closed via t.Cleanup.
func newTestFetchClient(t *testing.T) *kgo.Client {
	t.Helper()

	client, err := kgo.NewClient(kgo.SeedBrokers(rptest.Brokers()))
	require.NoError(t, err)
	t.Cleanup(client.Close)

	return client
}

// producedRecord is what a produce call told us actually happened: the key
// it wrote, and the partition and offset the broker assigned.
type producedRecord struct {
	key       []byte
	partition int32
	offset    int64
}

// produceRecords produces records and returns one producedRecord per input,
// in the same order, using the offsets and partitions the broker actually
// assigned.
func produceRecords(t *testing.T, client *kgo.Client, records []*kgo.Record) []producedRecord {
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
		})
	}
	return produced
}

// produceFillerRecords produces count keyed records of valueSize random
// bytes each, in chunks of fillerChunkSize separate ProduceSync calls, to
// roll segments past a topic's segment.bytes/segment.ms so compaction or
// retention has something to act on. Two things matter here beyond the
// record count: the bytes must be random rather than repeated - the
// producer's default Snappy compression collapses a repeated-byte payload
// to near nothing on disk, defeating segment.bytes entirely - and the
// produces must be chunked rather than sent as one giant batch, since the
// broker only evaluates segment rollover between appends; a single
// ProduceSync call landing as one append never rolls a segment no matter
// how large its logical payload is.
func produceFillerRecords(
	t *testing.T,
	client *kgo.Client,
	topic, keyPrefix string,
	count, valueSize int,
) {
	t.Helper()

	for start := 0; start < count; start += fillerChunkSize {
		end := start + fillerChunkSize
		if end > count {
			end = count
		}

		records := make([]*kgo.Record, 0, end-start)
		for i := start; i < end; i++ {
			value := make([]byte, valueSize)
			_, err := rand.Read(value)
			require.NoError(t, err)

			records = append(records, &kgo.Record{
				Topic: topic,
				Key:   []byte(fmt.Sprintf("%s-%04d", keyPrefix, i)),
				Value: value,
			})
		}
		produceRecords(t, client, records)
	}
}

// waitForIndexed polls until key is indexed and returns its pointer.
func waitForIndexed(t *testing.T, fx testFetchFixture, key []byte) index.Pointer {
	t.Helper()

	require.Eventually(t, func() bool {
		lookup, err := fx.index.Get(key)
		return err == nil && lookup.Found
	}, testEventually, testEventuallyTick, "key %q was not indexed", key)

	lookup, err := fx.index.Get(key)
	require.NoError(t, err)
	return lookup.Pointer
}

// fetchAtBounded calls FetchAt under a context bounded by testFetchTimeout,
// owning the timeout so callers don't repeat the WithTimeout/cancel dance.
func fetchAtBounded(
	t *testing.T,
	fx testFetchFixture,
	ptr index.Pointer,
	key []byte,
) (fetch.Result, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), testFetchTimeout)
	defer cancel()

	return fx.fetcher.FetchAt(ctx, ptr, key)
}

// assertMutuallyExclusive asserts that a Result never reports both
// Superseded and Evicted.
func assertMutuallyExclusive(t *testing.T, res fetch.Result) {
	t.Helper()

	assert.False(t, res.Superseded && res.Evicted, "superseded and evicted both true")
}

func TestFetcherIntegration(t *testing.T) {
	t.Run("round-trip byte-identical", func(t *testing.T) {
		fx := newTestFetchFixture(t, testPartitions, nil)

		const n = 30
		keys := make([]string, n)
		values := make([][]byte, n)
		records := make([]*kgo.Record, n)
		for i := range keys {
			keys[i] = fmt.Sprintf("roundtrip-key-%03d", i)
			values[i] = []byte(fmt.Sprintf("roundtrip-value-%03d", i))
			records[i] = &kgo.Record{Topic: fx.topic, Key: []byte(keys[i]), Value: values[i]}
		}

		produced := produceRecords(t, fx.producer, records)

		wantLastOffsets := make(map[int32]int64)
		for _, r := range produced {
			if cur, ok := wantLastOffsets[r.partition]; !ok || r.offset > cur {
				wantLastOffsets[r.partition] = r.offset
			}
		}

		require.Eventually(t, func() bool {
			for p, wantOffset := range wantLastOffsets {
				got, err := fx.index.Checkpoint(p)
				if err != nil || got != wantOffset {
					return false
				}
			}
			return true
		}, testEventually, testEventuallyTick, "partitions did not converge to produced offsets")

		for i, key := range keys {
			ptr := waitForIndexed(t, fx, []byte(key))

			res, err := fetchAtBounded(t, fx, ptr, []byte(key))
			require.NoError(t, err)

			assertMutuallyExclusive(t, res)
			assert.Equal(t, values[i], res.Value, "key %q", key)
			assert.False(t, res.Superseded, "key %q", key)
			assert.False(t, res.Evicted, "key %q", key)
		}
	})

	t.Run("superseded after compaction", func(t *testing.T) {
		configs := map[string]*string{
			"cleanup.policy":        kadm.StringPtr("compact"),
			"max.compaction.lag.ms": kadm.StringPtr("100"),
			"segment.ms":            kadm.StringPtr("100"),
			"segment.bytes":         kadm.StringPtr("1024"),
		}
		fx := newTestFetchFixture(t, 1, configs)

		key := []byte("compaction-key")
		v1 := []byte("compaction-value-v1")
		produceRecords(t, fx.producer, []*kgo.Record{{Topic: fx.topic, Key: key, Value: v1}})

		v1Pointer := waitForIndexed(t, fx, key)

		v2 := []byte("compaction-value-v2")
		produceRecords(t, fx.producer, []*kgo.Record{{Topic: fx.topic, Key: key, Value: v2}})
		produceFillerRecords(
			t,
			fx.producer,
			fx.topic,
			"compaction-filler",
			fillerRecordCount,
			fillerRecordBytes,
		)

		var lastErr error
		var lastRes fetch.Result
		require.Eventually(t, func() bool {
			res, err := fetchAtBounded(t, fx, v1Pointer, key)
			if err != nil {
				lastErr = err
				t.Logf("FetchAt error: %v", err)
				return false
			}
			lastErr = nil
			lastRes = res

			assertMutuallyExclusive(t, res)
			return res.Superseded
		}, testEventuallyLong, testEventuallyLongTick, "v1 pointer was never observed as superseded")

		require.NoError(t, lastErr)
		assert.True(t, lastRes.Superseded)
	})

	t.Run("evicted by retention", func(t *testing.T) {
		configs := map[string]*string{
			"cleanup.policy": kadm.StringPtr("delete"),
			"retention.ms":   kadm.StringPtr("100"),
			"segment.ms":     kadm.StringPtr("100"),
			"segment.bytes":  kadm.StringPtr("1024"),
		}
		fx := newTestFetchFixture(t, 1, configs)

		key := []byte("retention-key")
		value := []byte("retention-value")
		produceRecords(t, fx.producer, []*kgo.Record{{Topic: fx.topic, Key: key, Value: value}})

		ptr := waitForIndexed(t, fx, key)
		require.Equal(t, int64(0), ptr.Offset, "expected the first produced record at offset 0")

		produceFillerRecords(
			t,
			fx.producer,
			fx.topic,
			"retention-filler",
			fillerRecordCount,
			fillerRecordBytes,
		)

		var lastErr error
		var lastRes fetch.Result
		trickleCount := 0
		require.Eventually(t, func() bool {
			// retention.ms=100 is aggressive enough that the whole topic
			// goes empty, not just the segment holding ptr, once
			// production stops: every closed segment ages out within one
			// housekeeping pass. Fetching a stale offset against a fully
			// empty partition never resolves - keep a trickle of fresh
			// records flowing so the log always has a live tail while the
			// segment holding ptr ages out from under it.
			trickleValue := make([]byte, fillerRecordBytes)
			if _, err := rand.Read(trickleValue); err != nil {
				lastErr = err
				return false
			}
			trickleRecord := &kgo.Record{
				Topic: fx.topic,
				Key:   []byte(fmt.Sprintf("retention-trickle-%05d", trickleCount)),
				Value: trickleValue,
			}
			trickleCount++
			if err := fx.producer.ProduceSync(context.Background(), trickleRecord).FirstErr(); err != nil {
				lastErr = err
				return false
			}

			res, err := fetchAtBounded(t, fx, ptr, key)
			if err != nil {
				lastErr = err
				t.Logf("FetchAt error: %v", err)
				return false
			}
			lastErr = nil
			lastRes = res

			assertMutuallyExclusive(t, res)
			return res.Evicted
		}, testEventuallyLong, testEventuallyLongTick, "record was never observed as evicted")

		require.NoError(t, lastErr)
		assert.True(t, lastRes.Evicted)
	})
}
