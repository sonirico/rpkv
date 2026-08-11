// Package config parses the rpkv process configuration per SPEC "cmd/rpkv
// configuration"; a set flag wins over its env fallback, env wins over the
// hard default.
package config

import (
	"errors"
	"flag"
	"strings"

	"github.com/sonirico/vago/ent"
)

const (
	defaultDataDir = "./rpkv-data"
	defaultListen  = ":8080"
)

// Config holds the process configuration for cmd/rpkv.
type Config struct {
	Brokers []string
	Topics  []string
	DataDir string
	Listen  string
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

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if *brokers == "" {
		return Config{}, errors.New("config: brokers required (--brokers or RPKV_BROKERS)")
	}
	if *topics == "" {
		return Config{}, errors.New("config: topics required (--topics or RPKV_TOPICS)")
	}

	return Config{
		Brokers: splitList(*brokers),
		Topics:  splitList(*topics),
		DataDir: *dataDir,
		Listen:  *listen,
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
