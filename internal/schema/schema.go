package schema

import (
	"encoding/json"
	"strconv"
	"time"
)

const Version = 1

const (
	TypeSnapshot = "snapshot"
	TypeError    = "error"
)

const (
	ProviderClaude = "claude"
	ProviderCodex  = "codex"
)

const (
	SourceOAuth      = "oauth"
	SourceCache      = "cache"
	SourceTranscript = "transcript"
	SourceStdin      = "stdin"
)

type AuthStatus string

const (
	AuthOK      AuthStatus = "ok"
	AuthExpired AuthStatus = "expired"
	AuthMissing AuthStatus = "missing"
	AuthNoPlan  AuthStatus = "no_plan"
)

type Window struct {
	Utilization float64   `json:"utilization"`
	ResetsAt    time.Time `json:"resets_at,omitzero"`
	Suspect     bool      `json:"suspect,omitempty"`
}

type ExtraUsage struct {
	Utilization *float64 `json:"utilization,omitempty"`
}

type Snapshot struct {
	FetchedAt    time.Time          `json:"fetched_at"`
	FiveHour     *Window            `json:"five_hour,omitempty"`
	SevenDay     *Window            `json:"seven_day,omitempty"`
	ScopedLimits map[string]*Window `json:"scoped_limits,omitempty"`
	ExtraUsage   *ExtraUsage        `json:"extra_usage,omitempty"`
}

type snapshotWire struct {
	FetchedAt     time.Time          `json:"fetched_at"`
	FiveHour      *Window            `json:"five_hour,omitempty"`
	SevenDay      *Window            `json:"seven_day,omitempty"`
	SevenDayOpus  *Window            `json:"seven_day_opus,omitempty"`
	SevenDayFable *Window            `json:"seven_day_fable,omitempty"`
	ScopedLimits  map[string]*Window `json:"scoped_limits,omitempty"`
	ExtraUsage    *ExtraUsage        `json:"extra_usage,omitempty"`
}

func (s Snapshot) MarshalJSON() ([]byte, error) {
	w := snapshotWire{
		FetchedAt:    s.FetchedAt,
		FiveHour:     s.FiveHour,
		SevenDay:     s.SevenDay,
		ScopedLimits: s.ScopedLimits,
		ExtraUsage:   s.ExtraUsage,
	}
	if v, ok := s.ScopedLimits["Opus"]; ok {
		w.SevenDayOpus = v
	}
	if v, ok := s.ScopedLimits["Fable"]; ok {
		w.SevenDayFable = v
	}
	return json.Marshal(w)
}

type LimitHit struct {
	ResetsAt   *time.Time `json:"resets_at,omitempty"`
	Message    string     `json:"message,omitempty"`
	DetectedAt time.Time  `json:"detected_at"`
}

func (lh *LimitHit) Active(now time.Time) bool {
	return lh != nil && (lh.ResetsAt == nil || lh.ResetsAt.After(now))
}

type State struct {
	SchemaVersion int        `json:"schema_version"`
	Provider      string     `json:"provider,omitempty"`
	Type          string     `json:"type"`
	Source        string     `json:"source"`
	Stale         bool       `json:"stale"`
	StaleAge      *Duration  `json:"stale_age,omitempty"`
	Auth          AuthStatus `json:"auth"`
	Snapshot      *Snapshot  `json:"snapshot,omitempty"`
	LimitHit      *LimitHit  `json:"limit_hit,omitempty"`
	Error         string     `json:"error,omitempty"`
	Drift         []string   `json:"drift,omitempty"`
}

type Duration time.Duration

func (d Duration) Seconds() float64 { return time.Duration(d).Seconds() }

func (d Duration) MarshalJSON() ([]byte, error) {
	return []byte(strconv.FormatFloat(d.Seconds(), 'f', -1, 64)), nil
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	secs, err := strconv.ParseFloat(string(b), 64)
	if err != nil {
		return err
	}
	*d = Duration(time.Duration(secs * float64(time.Second)))
	return nil
}

func NewDuration(d time.Duration) *Duration {
	wd := Duration(d)
	return &wd
}
