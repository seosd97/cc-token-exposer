package engine

import (
	"time"

	"github.com/seosd97/cc-token-exposer/internal/schema"
)

func snapshotState(snap *schema.Snapshot, source string, stale bool, staleAge time.Duration, auth schema.AuthStatus) *schema.State {
	st := &schema.State{
		SchemaVersion: schema.Version,
		Type:          schema.TypeSnapshot,
		Source:        source,
		Stale:         stale,
		Auth:          auth,
		Snapshot:      snap,
	}
	if stale && staleAge > 0 {
		st.StaleAge = schema.NewDuration(staleAge)
	}
	return st
}

func transcriptState(lh *schema.LimitHit, auth schema.AuthStatus) *schema.State {
	return &schema.State{
		SchemaVersion: schema.Version,
		Type:          schema.TypeSnapshot,
		Source:        schema.SourceTranscript,
		Stale:         true,
		Auth:          auth,
		LimitHit:      lh,
	}
}

func errorState(auth schema.AuthStatus, msg string) *schema.State {
	return &schema.State{
		SchemaVersion: schema.Version,
		Type:          schema.TypeError,
		Auth:          auth,
		Error:         msg,
	}
}
