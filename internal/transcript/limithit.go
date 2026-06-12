// Package transcript detects session-limit events in Claude Code's local JSONL transcripts.
package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type LimitHit struct {
	ResetsAt   time.Time
	HasReset   bool
	DetectedAt time.Time
}

const maxLineBytes = 8 << 20

var resetRe = regexp.MustCompile(`(?i)resets?\s+(\d{1,2})(?::(\d{2}))?\s*([ap])\.?\s*m\.?`)

var limitRe = regexp.MustCompile(`(?i)\bsession limit\b`)

var tzRe = regexp.MustCompile(`\(([A-Za-z]+(?:/[A-Za-z0-9_+\-]+)+)\)`)

// ParseReset parses a "resets H[:MM]am/pm" clock time out of text and resolves
// it to the next future instant in loc relative to ref, rolling forward one day
// if needed; an explicit "(Area/City)" timezone annotation overrides loc.
func ParseReset(text string, ref time.Time, loc *time.Location) (time.Time, bool) {
	if loc == nil {
		loc = time.Local
	}
	if tm := tzRe.FindStringSubmatch(text); tm != nil {
		if l, err := time.LoadLocation(tm[1]); err == nil {
			loc = l
		}
	}
	m := resetRe.FindStringSubmatch(text)
	if m == nil {
		return time.Time{}, false
	}
	hour, err := strconv.Atoi(m[1])
	if err != nil || hour < 1 || hour > 12 {
		return time.Time{}, false
	}
	min := 0
	if m[2] != "" {
		min, err = strconv.Atoi(m[2])
		if err != nil || min > 59 {
			return time.Time{}, false
		}
	}
	h := hour % 12
	if strings.EqualFold(m[3], "p") {
		h += 12
	}

	refLocal := ref.In(loc)
	cand := time.Date(refLocal.Year(), refLocal.Month(), refLocal.Day(), h, min, 0, 0, loc)
	if !cand.After(refLocal) {
		cand = cand.AddDate(0, 0, 1)
	}
	return cand, true
}

type transcriptLine struct {
	Timestamp         string          `json:"timestamp"`
	IsAPIErrorMessage bool            `json:"isApiErrorMessage"`
	Message           json.RawMessage `json:"message"`
}

func ScanReader(r io.Reader, now time.Time, loc *time.Location) (*LimitHit, error) {
	if loc == nil {
		loc = time.Local
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)

	var latest *LimitHit
	for sc.Scan() {
		hit := detectLine(sc.Bytes(), now, loc)
		if hit == nil {
			continue
		}
		if latest == nil || hit.DetectedAt.After(latest.DetectedAt) {
			latest = hit
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("transcript: scan: %w", err)
	}
	return latest, nil
}

func ScanFile(path string, now time.Time, loc *time.Location) (*LimitHit, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("transcript: open: %w", err)
	}
	defer func() { _ = f.Close() }()
	return ScanReader(f, now, loc)
}

func ScanLatest(paths []string, now time.Time, loc *time.Location) (*LimitHit, error) {
	var latest *LimitHit
	for _, p := range paths {
		hit, err := ScanFile(p, now, loc)
		if err != nil {
			continue
		}
		if hit == nil {
			continue
		}
		if latest == nil || hit.DetectedAt.After(latest.DetectedAt) {
			latest = hit
		}
	}
	return latest, nil
}

func detectLine(raw []byte, now time.Time, loc *time.Location) *LimitHit {
	if !bytes.Contains(raw, []byte("isApiErrorMessage")) || !bytes.Contains(raw, []byte("limit")) {
		return nil
	}

	var tl transcriptLine
	if err := json.Unmarshal(raw, &tl); err != nil {
		return nil
	}
	if !tl.IsAPIErrorMessage {
		return nil
	}

	text := messageText(tl.Message)
	if text == "" {
		text = string(raw)
	}
	if !limitRe.MatchString(text) {
		return nil
	}

	ref := now
	if t, err := time.Parse(time.RFC3339, tl.Timestamp); err == nil {
		ref = t
	}

	hit := &LimitHit{DetectedAt: ref}
	if resetsAt, ok := ParseReset(text, ref, loc); ok {
		hit.ResetsAt = resetsAt
		hit.HasReset = true
	}
	return hit
}

// messageText extracts the text of a transcript message, whose content may be
// a plain string or an array of typed blocks; returns "" when neither.
func messageText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var env struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || len(env.Content) == 0 {
		return ""
	}

	var s string
	if json.Unmarshal(env.Content, &s) == nil {
		return s
	}

	var blocks []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(env.Content, &blocks) == nil {
		var b strings.Builder
		for _, bl := range blocks {
			if bl.Text != "" {
				b.WriteString(bl.Text)
				b.WriteByte(' ')
			}
		}
		return strings.TrimSpace(b.String())
	}
	return ""
}
