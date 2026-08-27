// Package rank orders groups by how relevant they are to the current
// directory. It always sorts and never filters: every candidate comes back.
package rank

import (
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Candidate is one group being ranked.
type Candidate struct {
	Name       string
	Paths      []string
	UseCount   int
	LastUsedAt time.Time
}

// Score bands. Containment dominates frecency by construction: standing in a
// repo is a far stronger signal than having used something a lot last week.
const (
	insideRepoBase = 1_000_000_000.0
	parentOfRepo   = 1_000_000.0
)

// Rank returns candidate names, most relevant first.
func Rank(candidates []Candidate, cwd string, now time.Time) []string {
	cwd = filepath.Clean(cwd)

	type scored struct {
		name  string
		score float64
	}

	out := make([]scored, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, scored{name: c.Name, score: score(c, cwd, now)})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].name < out[j].name
	})

	names := make([]string, 0, len(out))
	for _, s := range out {
		names = append(names, s.name)
	}
	return names
}

func score(c Candidate, cwd string, now time.Time) float64 {
	best := 0.0

	for _, p := range c.Paths {
		p = filepath.Clean(p)
		switch {
		case contains(p, cwd):
			// Deeper match wins, so a nested repo beats its parent directory.
			if s := insideRepoBase + float64(len(p)); s > best {
				best = s
			}
		case contains(cwd, p):
			if parentOfRepo > best {
				best = parentOfRepo
			}
		}
	}
	if best > 0 {
		return best
	}

	return float64(c.UseCount) * decay(now.Sub(c.LastUsedAt))
}

// contains reports whether child is dir itself or lives beneath it. It compares
// whole path segments, so /code/xyz does not contain /code/xyz-old.
func contains(dir, child string) bool {
	return child == dir || strings.HasPrefix(child, dir+string(filepath.Separator))
}

// decay is the zoxide-style frecency curve.
func decay(age time.Duration) float64 {
	switch {
	case age < time.Hour:
		return 4
	case age < 24*time.Hour:
		return 2
	case age < 7*24*time.Hour:
		return 0.5
	default:
		return 0.25
	}
}
