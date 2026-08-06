package transcript

import (
	"time"

	"github.com/seosd97/cc-token-exposer/internal/schema"
)

const limitMessage = "session limit reached"

const defaultMaxFiles = 64

type Probe struct {
	dir      func() (string, error)
	loc      *time.Location
	maxFiles int
}

type ProbeOption func(*Probe)

func WithLocation(loc *time.Location) ProbeOption {
	return func(p *Probe) {
		if loc != nil {
			p.loc = loc
		}
	}
}

func WithMaxFiles(n int) ProbeOption {
	return func(p *Probe) {
		if n > 0 {
			p.maxFiles = n
		}
	}
}

func WithProjectsDir(dir string) ProbeOption {
	return func(p *Probe) {
		if dir != "" {
			p.dir = func() (string, error) { return dir, nil }
		}
	}
}

func NewProbe(opts ...ProbeOption) *Probe {
	p := &Probe{
		dir: func() (string, error) {
			return DefaultProjectsDir()
		},
		loc:      time.Local,
		maxFiles: defaultMaxFiles,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func (p *Probe) Probe(now time.Time) (*schema.LimitHit, error) {
	dir, err := p.dir()
	if err != nil || dir == "" {
		return nil, nil
	}
	files, err := FindTranscripts(dir, p.maxFiles)
	if err != nil || len(files) == 0 {
		return nil, nil
	}

	hit, err := ScanLatest(files, now, p.loc)
	if err != nil || hit == nil {
		return nil, nil
	}
	if hit.HasReset && !hit.ResetsAt.After(now) {
		return nil, nil
	}

	out := &schema.LimitHit{
		Message:    limitMessage,
		DetectedAt: hit.DetectedAt,
	}
	if hit.HasReset {
		resetsAt := hit.ResetsAt
		out.ResetsAt = &resetsAt
	}
	return out, nil
}
