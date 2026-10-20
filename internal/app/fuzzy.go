package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/sahilm/fuzzy"
)

// Match is one search hit.
type Match struct {
	App            App
	Rank           int   // 0 = exact bundle id, 1 = exact name (case-insensitive), 2 = name prefix, 3 = fuzzy
	Score          int   // from fuzzy for Rank 3; a large constant for ranks 0-2
	MatchedIndexes []int // rune indexes into App.Name to highlight (nil for ranks 0-1 and bundle-id hits)
}

// exactScore is the Score given to ranks 0-2 so they always outrank fuzzy hits.
const exactScore = 1 << 20

// ErrNotFound is returned by Resolve when nothing matches the argument.
var ErrNotFound = errors.New("app: not found")

// AmbiguousError is returned by Resolve when an argument matches several apps.
type AmbiguousError struct {
	Arg        string
	Candidates []Match // at most 10, best first
}

func (e *AmbiguousError) Error() string {
	names := make([]string, len(e.Candidates))
	for i, m := range e.Candidates {
		names[i] = m.App.Name
	}
	return fmt.Sprintf("app: %q is ambiguous: %s", e.Arg, strings.Join(names, ", "))
}

// fields is a fuzzy.Source exposing three strings per app: name, the last
// segment of the bundle id, and the full bundle id.
type fields []App

func (f fields) Len() int { return len(f) * 3 }

func (f fields) String(i int) string {
	a := f[i/3]
	switch i % 3 {
	case 0:
		return a.Name
	case 1:
		return a.BundleID[strings.LastIndex(a.BundleID, ".")+1:]
	default:
		return a.BundleID
	}
}

// Fuzzy returns apps matching q, best first. Empty q returns every app in the
// input order wrapped as Rank 3 / Score 0 with no highlights. Matching considers
// Name primarily; the bundle id's last segment and the full bundle id are also
// searched so "vscode" finds "Visual Studio Code" (com.microsoft.VSCode).
// Ties within a rank sort by Score desc, then Name asc (case-insensitive).
func Fuzzy(apps []App, q string) []Match {
	out := make([]Match, 0, len(apps))
	if q == "" {
		for _, a := range apps {
			out = append(out, Match{App: a, Rank: 3})
		}
		return out
	}

	// Cheap pass for ranks 0-2.
	seen := make(map[int]bool, len(apps))
	lq := strings.ToLower(q)
	for i, a := range apps {
		switch {
		case a.BundleID != "" && a.BundleID == q:
			out = append(out, Match{App: a, Rank: 0, Score: exactScore})
		case strings.EqualFold(a.Name, q):
			out = append(out, Match{App: a, Rank: 1, Score: exactScore})
		case strings.HasPrefix(strings.ToLower(a.Name), lq):
			idx := make([]int, utf8.RuneCountInString(q))
			for j := range idx {
				idx[j] = j
			}
			out = append(out, Match{App: a, Rank: 2, Score: exactScore, MatchedIndexes: idx})
		default:
			continue
		}
		seen[i] = true
	}

	// Fuzzy pass over name + bundle id fields; keep the best score per app.
	best := make(map[int]*Match)
	for _, m := range fuzzy.FindFrom(q, fields(apps)) {
		i := m.Index / 3
		if seen[i] {
			continue
		}
		cur, ok := best[i]
		if !ok {
			cur = &Match{App: apps[i], Rank: 3, Score: m.Score}
			best[i] = cur
		} else if m.Score > cur.Score {
			cur.Score = m.Score
		}
		if m.Index%3 == 0 {
			cur.MatchedIndexes = runeIndexes(apps[i].Name, m.MatchedIndexes)
		}
	}
	for _, m := range best {
		out = append(out, *m)
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Rank != b.Rank {
			return a.Rank < b.Rank
		}
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		return strings.ToLower(a.App.Name) < strings.ToLower(b.App.Name)
	})
	return out
}

// runeIndexes converts byte offsets into s to rune indexes.
func runeIndexes(s string, bytes []int) []int {
	out := make([]int, len(bytes))
	for i, b := range bytes {
		out[i] = utf8.RuneCountInString(s[:b])
	}
	return out
}

// Resolve picks the app meant by a CLI argument: exact bundle id, else exact
// name (case-insensitive), else exact path, else the single fuzzy match with
// Rank <= 2. Otherwise it returns *AmbiguousError listing up to 10 candidates,
// or ErrNotFound when nothing matches at all.
func Resolve(apps []App, arg string) (App, error) {
	for _, a := range apps {
		if a.BundleID != "" && a.BundleID == arg {
			return a, nil
		}
	}
	for _, a := range apps {
		if strings.EqualFold(a.Name, arg) {
			return a, nil
		}
	}
	clean := filepath.Clean(arg)
	for _, a := range apps {
		if a.Path != "" && a.Path == clean {
			return a, nil
		}
	}

	matches := Fuzzy(apps, arg)
	if len(matches) == 0 {
		return App{}, ErrNotFound
	}
	var strong []Match
	for _, m := range matches {
		if m.Rank <= 2 {
			strong = append(strong, m)
		}
	}
	if len(strong) == 1 {
		return strong[0].App, nil
	}
	if len(matches) > 10 {
		matches = matches[:10]
	}
	return App{}, &AmbiguousError{Arg: arg, Candidates: matches}
}
