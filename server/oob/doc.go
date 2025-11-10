// Package oob provides minimal, generic primitives to manage out-of-band
// (OOB) interactions using typed pending entries bound to a namespace.
//
// The package centralizes only the lifecycle and remapping concerns:
// - Create a pending interaction tied to the caller's namespace
// - Look up by ID and map back to the correct namespace
// - Complete or cancel a pending interaction
// - Minimal HTTP helper to inject the correct namespace into context based on ID
//
// UI and flow specifics (device code pages, forms, redirects) are left entirely
// to the service. This keeps services free to present OOB in any way while
// guaranteeing that callbacks are mapped to the right namespace.
package oob
