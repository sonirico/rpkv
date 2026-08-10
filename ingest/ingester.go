// Package ingest consumes a topic over the public Kafka protocol and
// projects it into the index. No consumer groups: partition assignment is
// direct and resume state is the index's own checkpoints.
package ingest

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/sonirico/rpkv/index"
)

// Ingester consumes a topic's partitions directly (no consumer group) and
// applies fetched records to the index, resuming after each partition's
// last applied checkpoint.
type Ingester struct {
	client          *kgo.Client
	index           *index.Index
	topic           string
	logger          *slog.Logger
	skippedNullKeys atomic.Int64
}

// NewIngester wires an already-configured kgo client and index into an
// Ingester. It does not open or configure either - that is the caller's
// responsibility.
func NewIngester(
	client *kgo.Client,
	index *index.Index,
	topic string,
	logger *slog.Logger,
) *Ingester {
	return &Ingester{
		client: client,
		index:  index,
		topic:  topic,
		logger: logger,
	}
}

// Run discovers the topic's partitions, assigns them for direct
// consumption starting at each partition's checkpoint+1 (or the log start
// when no checkpoint exists), and polls until ctx is done. Each poll
// applies one atomic index.Apply per partition carrying that partition's
// records and its advanced checkpoint.
func (in *Ingester) Run(ctx context.Context) error {
	admin := kadm.NewClient(in.client)

	topicDetails, err := admin.ListTopics(ctx, in.topic)
	if err != nil {
		return fmt.Errorf("ingest: list topic %q: %w", in.topic, err)
	}
	detail, ok := topicDetails[in.topic]
	if !ok {
		return fmt.Errorf("ingest: list topic %q: not found", in.topic)
	}
	if detail.Err != nil {
		return fmt.Errorf("ingest: list topic %q: %w", in.topic, detail.Err)
	}

	offsets := make(map[int32]kgo.Offset, len(detail.Partitions))
	for partition := range detail.Partitions {
		checkpoint, err := in.index.Checkpoint(partition)
		if err != nil {
			return fmt.Errorf("ingest: checkpoint partition %d: %w", partition, err)
		}
		offsets[partition] = resumeOffset(checkpoint)
	}

	in.client.AddConsumePartitions(map[string]map[int32]kgo.Offset{in.topic: offsets})

	for {
		fetches := in.client.PollFetches(ctx)
		if err := ctx.Err(); err != nil {
			return err
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			e := errs[0]
			return fmt.Errorf("ingest: fetch %s/%d: %w", e.Topic, e.Partition, e.Err)
		}

		var applyErr error
		fetches.EachPartition(func(ftp kgo.FetchTopicPartition) {
			if applyErr != nil || len(ftp.Records) == 0 {
				return
			}
			entries, skipped := collectEntries(ftp.Records)
			in.skippedNullKeys.Add(skipped)
			checkpoint := ftp.Records[len(ftp.Records)-1].Offset
			if err := in.index.Apply(entries, map[int32]int64{ftp.Partition: checkpoint}); err != nil {
				applyErr = fmt.Errorf("ingest: apply partition %d: %w", ftp.Partition, err)
				return
			}
			in.logger.Debug(
				"ingest: applied partition batch",
				"partition", ftp.Partition,
				"records", len(ftp.Records),
				"skipped", skipped,
				"checkpoint", checkpoint,
			)
		})
		if applyErr != nil {
			return applyErr
		}
	}
}

// SkippedNullKeys reports how many null-key records have been skipped
// since construction. Safe for concurrent use.
func (in *Ingester) SkippedNullKeys() int64 {
	return in.skippedNullKeys.Load()
}

// resumeOffset maps an index checkpoint to the kgo offset to resume from:
// checkpoint+1, or the partition's log start when no checkpoint (-1)
// exists.
func resumeOffset(checkpoint int64) kgo.Offset {
	if checkpoint < 0 {
		return kgo.NewOffset().AtStart()
	}
	return kgo.NewOffset().At(checkpoint + 1)
}
