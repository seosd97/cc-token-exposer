package transcript

import (
	"time"

	"github.com/seosd97/cc-token-exposer/internal/schema"
)

const limitMessage = "session limit reached"

const defaultMaxFiles = 64

type Probe struct {
	resolve  func() ([]string, error)
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
			p.resolve = func() ([]string, error) { return FindTranscripts(dir) }
		}
	}
}

func NewProbe(opts ...ProbeOption) *Probe {
	p := &Probe{
		resolve: func() ([]string, error) {
			dir, err := DefaultProjectsDir()
			if err != nil {
				return nil, err
			}
			return FindTranscripts(dir)
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
	paths, err := p.resolve()
	if err != nil || len(paths) == 0 {
		return nil, nil
	}
	if p.maxFiles > 0 && len(paths) > p.maxFiles {
		paths = paths[:p.maxFiles]
	}

	hit, err := ScanLatest(paths, now, p.loc)
	if err != nil || hit == nil {
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
