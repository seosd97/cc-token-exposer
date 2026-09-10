package usage

import "github.com/seosd97/cc-token-exposer/internal/schema"

type FetchedSnapshot struct {
	Snapshot     *schema.Snapshot
	ScopedProbed bool
	Drift        []string
}
