package usage

import (
	"bytes"
	"encoding/json"
	"time"
)

func ParseTolerantTime(raw json.RawMessage) time.Time {
	if len(bytes.TrimSpace(raw)) == 0 {
		return time.Time{}
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t
		}
		return time.Time{}
	}
	var n float64
	if json.Unmarshal(raw, &n) == nil && n > 0 {
		if n > 1e12 {
			return time.UnixMilli(int64(n)).UTC()
		}
		return time.Unix(int64(n), 0).UTC()
	}
	return time.Time{}
}
