// Package anomaly loads and evaluates immutable Isolation Forest artifacts.
//
// The package only scores trusted monitoring features. Whether an alert may
// gate execution remains an explicit control-plane decision made outside this
// package.
package anomaly
