// Package usage implements the Anthropic OAuth usage HTTP client.
package usage

import "time"

type Window struct {
	Utilization float64   `json:"utilization"`
	ResetsAt    time.Time `json:"resets_at"`
	Suspect     bool      `json:"suspect,omitempty"`
}

type ExtraUsage struct {
	Utilization *float64 `json:"utilization,omitempty"`
}

type Snapshot struct {
	FetchedAt    time.Time   `json:"fetched_at"`
	FiveHour     *Window     `json:"five_hour,omitempty"`
	SevenDay     *Window     `json:"seven_day,omitempty"`
	SevenDayOpus *Window     `json:"seven_day_opus,omitempty"`
	ExtraUsage   *ExtraUsage `json:"extra_usage,omitempty"`
}
