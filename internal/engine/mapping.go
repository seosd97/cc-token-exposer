package engine

import (
	"time"

	"github.com/seosd97/cc-token-exposer/internal/schema"
)

func freshState(snap *schema.Snapshot, source string, drift []string) *schema.State {
	return &schema.State{
		SchemaVersion: schema.Version,
		Type:          schema.TypeSnapshot,
		Source:        source,
		Auth:          schema.AuthOK,
		Snapshot:      snap,
		Drift:         drift,
	}
}

func staleState(snap *schema.Snapshot, source string, age time.Duration, auth schema.AuthStatus, drift []string) *schema.State {
	st := &schema.State{
		SchemaVersion: schema.Version,
		Type:          schema.TypeSnapshot,
		Source:        source,
		Stale:         true,
		Auth:          auth,
		Snapshot:      snap,
		Drift:         drift,
	}
	if age > 0 {
		st.StaleAge = schema.NewDuration(age)
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
