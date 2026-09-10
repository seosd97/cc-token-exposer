// Package usage implements the Anthropic OAuth usage HTTP client.
package usage

import "github.com/seosd97/cc-token-exposer/internal/schema"

// FetchedSnapshot is a snapshot plus its API provenance: ScopedProbed is set
// only by a real endpoint decode (never by a stdin projection) and marks that
// the API answered about scoped limits, which the engine uses to judge stdin
// completeness. Drift lists wire-shape anomalies the decode detected in this
// response; nil when the shape matched expectations.
type FetchedSnapshot struct {
	Snapshot     *schema.Snapshot
	ScopedProbed bool
	Drift        []string
}
