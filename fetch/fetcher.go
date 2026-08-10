// Package fetch turns index pointers into value bytes: a single-record
// fetch at (partition, offset) over the public Kafka protocol, verified
// per SPEC "Compaction model" mechanism 2. The *kgo.Client passed to
// NewFetcher must be dedicated to this Fetcher: FetchAt drives the
// client's direct-consume assignment, which would corrupt an Ingester
// sharing the same client.
package fetch

import (
	"context"
	"fmt"
	"sync"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/sonirico/rpkv/index"
)

// Result is the outcome of a single-record fetch at a pointer's offset.
type Result struct {
	Value      []byte
	Superseded bool
	Evicted    bool
}

// Fetcher resolves a key's value from the log by fetching the single
// record at its index pointer, over a client dedicated to this purpose.
type Fetcher struct {
	client *kgo.Client
	topic  string
	mu     sync.Mutex
}

// NewFetcher wires an already-configured kgo client and topic into a
// Fetcher. It does not open or configure the client - that is the
// caller's responsibility.
func NewFetcher(client *kgo.Client, topic string) *Fetcher {
	return &Fetcher{
		client: client,
		topic:  topic,
	}
}

// FetchAt fetches the single record at ptr and verifies it against key
// per the read verification protocol. error is transport-level only; a
// verification outcome (value, superseded, evicted) is never an error.
func (f *Fetcher) FetchAt(ctx context.Context, ptr index.Pointer, key []byte) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.client.AddConsumePartitions(map[string]map[int32]kgo.Offset{
		f.topic: {ptr.Partition: kgo.NewOffset().At(ptr.Offset)},
	})
	defer f.client.RemoveConsumePartitions(map[string][]int32{f.topic: {ptr.Partition}})

	for {
		fetches := f.client.PollFetches(ctx)
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			e := errs[0]
			return Result{}, fmt.Errorf("fetch: poll %s/%d: %w", e.Topic, e.Partition, e.Err)
		}

		var (
			res      Result
			resolved bool
		)
		fetches.EachPartition(func(p kgo.FetchTopicPartition) {
			if resolved || p.Topic != f.topic || p.Partition != ptr.Partition {
				return
			}
			res, resolved = verifyFetch(p.Records, p.LogStartOffset, ptr, key)
		})
		if resolved {
			return res, nil
		}
	}
}
