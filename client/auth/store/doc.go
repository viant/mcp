// Package store defines simple token and client-configuration stores used by the
// authorization helpers in the parent `auth` package.
//
// It currently ships with an in-memory implementation that is sufficient for most
// CLI or unit-test scenarios but can be swapped for a persistent backend if
// required.
package store
