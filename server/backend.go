package server

import (
	"context"

	"github.com/sonirico/rpkv/fetch"
	"github.com/sonirico/rpkv/index"
)

// indexReader is the subset of *index.Index the server reads from.
type indexReader interface {
	Get(key []byte) (index.Lookup, error)
	Checkpoint(partition int32) (int64, error)
}

// valueFetcher is the subset of *fetch.Fetcher the server reads from.
type valueFetcher interface {
	FetchAt(ctx context.Context, ptr index.Pointer, key []byte) (fetch.Result, error)
}

// offsetSource provides the per-partition log end offsets for a topic, used
// to compute healthz lag.
type offsetSource interface {
	LogEndOffsets(ctx context.Context) (map[int32]int64, error)
}

// Backend bundles the per-topic dependencies the server reads from.
type Backend struct {
	index   indexReader
	fetcher valueFetcher
	offsets offsetSource
	shape   shapeSource
}

// NewBackend wires an already-built index reader, value fetcher, offset
// source and shape source into a Backend.
func NewBackend(ix indexReader, f valueFetcher, o offsetSource, s shapeSource) Backend {
	return Backend{index: ix, fetcher: f, offsets: o, shape: s}
}
