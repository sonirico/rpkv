// Package config parses the rpkv process configuration per SPEC "cmd/rpkv
// configuration"; a set flag wins over its env fallback, env wins over the
// hard default.
package config

import (
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/sonirico/vago/ent"
)

const (
	defaultDataDir         = "./rpkv-data"
	defaultListen          = ":8080"
	defaultMetadataRefresh = "30s"
)

// Config holds the process configuration for cmd/rpkv.
type Config struct {
	Brokers         []string
	Topics          []string
	DataDir         string
	Listen          string
	MetadataRefresh time.Duration
}

// Parse reads the configuration from args per SPEC: each flag falls back
// to its environment variable when unset (flag wins), and brokers and
// topics are required.
func Parse(args []string) (Config, error) {
	fs := flag.NewFlagSet("rpkv", flag.ContinueOnError)
	brokers := fs.String(
		"brokers",
		ent.Get("RPKV_BROKERS", ""),
		"comma-separated broker addresses (env RPKV_BROKERS)",
	)
	topics := fs.String(
		"topics",
		ent.Get("RPKV_TOPICS", ""),
		"comma-separated topics to index (env RPKV_TOPICS)",
	)
	dataDir := fs.String(
		"data-dir",
		ent.Get("RPKV_DATA_DIR", defaultDataDir),
		"index data directory (env RPKV_DATA_DIR)",
	)
	listen := fs.String(
		"listen",
		ent.Get("RPKV_LISTEN", defaultListen),
		"HTTP listen address (env RPKV_LISTEN)",
	)
	metadataRefresh := fs.String(
		"metadata-refresh",
		ent.Get("RPKV_METADATA_REFRESH", defaultMetadataRefresh),
		"partition metadata refresh interval (env RPKV_METADATA_REFRESH)",
	)

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if *brokers == "" {
		return Config{}, errors.New("config: brokers required (--brokers or RPKV_BROKERS)")
	}
	if *topics == "" {
		return Config{}, errors.New("config: topics required (--topics or RPKV_TOPICS)")
	}

	refresh, err := time.ParseDuration(*metadataRefresh)
	if err != nil {
		return Config{}, fmt.Errorf("config: metadata-refresh: %w", err)
	}
	if refresh <= 0 {
		return Config{}, errors.New("config: metadata-refresh must be positive")
	}

	return Config{
		Brokers:         splitList(*brokers),
		Topics:          splitList(*topics),
		DataDir:         *dataDir,
		Listen:          *listen,
		MetadataRefresh: refresh,
	}, nil
}

func splitList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
