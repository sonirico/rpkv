// Package metrics is the facade public packages use to emit metrics. It is
// the only metrics contract public packages see; implementations (Prometheus
// or otherwise) live in wiring, never in the packages that report through
// this interface.
package metrics

// Counter is a monotonically increasing value, structurally matching
// Prometheus's counter contract.
type Counter interface {
	// Inc increments the counter by 1.
	Inc()
	// Add increments the counter by delta.
	Add(delta float64)
}
