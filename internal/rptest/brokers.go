// Package rptest provides shared support for the integration tests that
// exercise a real Redpanda broker. The broker is self-provisioned per test
// run via testit/redpanda (ADR-006): each integration test binary's
// TestMain calls Main, and tests dial Brokers().
package rptest

import "os"

const brokersEnvVar = "RPKV_TEST_BROKERS"

// brokerAddr is the bootstrap address of the broker Main provisioned,
// empty until then.
var brokerAddr string

// Brokers returns the Kafka bootstrap address for integration tests.
// RPKV_TEST_BROKERS overrides; otherwise it is the address of the broker
// Main started for this test binary.
func Brokers() string {
	if v := os.Getenv(brokersEnvVar); v != "" {
		return v
	}
	return brokerAddr
}
