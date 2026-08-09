// Package rptest provides shared support for the integration tests that
// exercise a real Redpanda broker started by the dev loop (justfile
// redpanda-up).
package rptest

import "os"

// Brokers returns the Kafka bootstrap address for integration tests. It
// reads RPKV_TEST_BROKERS and falls back to the dev loop's external
// listener on localhost:19092 when the variable is unset.
func Brokers() string {
	if v := os.Getenv("RPKV_TEST_BROKERS"); v != "" {
		return v
	}
	return "localhost:19092"
}
