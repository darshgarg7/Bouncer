// Package monitoring implements deterministic runtime behavior monitors.
// Immutable statistical anomaly models consume the same windows after verified
// execution. They begin in shadow mode and may circuit-break subsequent actions
// only through an independently promoted artifact; these rules remain separate
// and explainable.
package monitoring
