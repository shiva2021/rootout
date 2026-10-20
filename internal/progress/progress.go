// Package progress reports the progress of long operations with a single
// updating terminal line. It stays silent when stdout is not a terminal so
// --json output and pipes remain clean.
package progress

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
)

// Reporter receives progress from a long operation. Implementations must be
// safe for concurrent Step calls (operations use worker pools).
type Reporter interface {
	// Start begins a new operation. total 0 means unknown: a spinner is shown
	// instead of a bar.
	Start(label string, total int)
	// Step marks one unit done; detail names the item just finished (may be "").
	Step(detail string)
	// Done clears the line. It is safe to call twice.
	Done()
}

// Nop returns a Reporter that does nothing.
func Nop() Reporter { return nop{} }

type nop struct{}

func (nop) Start(string, int) {}
func (nop) Step(string)       {}
func (nop) Done()             {}

// Recorder is a test helper for other packages: it records calls.
type Recorder struct {
	mu       sync.Mutex
	Label    string
	Total    int
	Steps    []string
	Finished bool
}

// Start records the label and total.
func (r *Recorder) Start(label string, total int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Label, r.Total = label, total
}

// Step appends detail to Steps.
func (r *Recorder) Step(detail string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Steps = append(r.Steps, detail)
}

// Done marks the recorder finished.
func (r *Recorder) Done() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Finished = true
}

const (
	barCells     = 20
	throttle     = 100 * time.Millisecond
	defaultWidth = 80
	clearLine    = "\r\033[K"
	ellipsis     = "…"
)

var spinner = []string{"⠋", "⠙", "⠹", "⠸"}

// Option configures the reporter returned by Term.
type Option func(*termReporter)

// ForceTTY renders even when the writer is not a terminal (tests).
func ForceTTY() Option { return func(t *termReporter) { t.tty = true } }

// Width sets the line width in columns. Default: terminal width via x/term,
// else 80.
func Width(cols int) Option {
	return func(t *termReporter) {
		if cols > 0 {
			t.width = cols
		}
	}
}

// Clock replaces the time source used by the redraw throttle (tests).
func Clock(now func() time.Time) Option {
	return func(t *termReporter) {
		if now != nil {
			t.now = now
		}
	}
}

// Term writes one updating line to w. It is a no-op when w is not a terminal
// (so --json and pipes stay clean) unless ForceTTY is set.
func Term(w io.Writer, opts ...Option) Reporter {
	t := &termReporter{w: w, now: time.Now}
	if f, ok := w.(*os.File); ok {
		fd := int(f.Fd())
		t.tty = term.IsTerminal(fd)
		if cols, _, err := term.GetSize(fd); err == nil && cols > 0 {
			t.width = cols
		}
	}
	for _, o := range opts {
		o(t)
	}
	if !t.tty {
		return Nop()
	}
	if t.width == 0 {
		t.width = defaultWidth
	}
	return t
}

type termReporter struct {
	w     io.Writer
	tty   bool
	width int
	now   func() time.Time

	mu       sync.Mutex
	active   bool
	label    string
	total    int
	done     int
	detail   string
	frame    int
	lastDraw time.Time
}

func (t *termReporter) Start(label string, total int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.active = true
	t.label, t.total, t.done, t.detail, t.frame = label, total, 0, "", 0
	t.draw()
}

func (t *termReporter) Step(detail string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active {
		return
	}
	t.done++
	t.detail = detail
	final := t.total > 0 && t.done >= t.total
	if !final && t.now().Sub(t.lastDraw) < throttle {
		return
	}
	t.draw()
}

func (t *termReporter) Done() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active {
		return
	}
	t.active = false
	_, _ = io.WriteString(t.w, clearLine)
}

// draw renders the current state. The caller holds t.mu.
func (t *termReporter) draw() {
	t.lastDraw = t.now()
	var head string
	if t.total > 0 {
		filled := t.done * barCells / t.total
		if filled > barCells {
			filled = barCells
		}
		bar := strings.Repeat("▰", filled) + strings.Repeat("▱", barCells-filled)
		head = fmt.Sprintf("%s %s %d/%d", t.label, bar, t.done, t.total)
	} else {
		head = fmt.Sprintf("%s %s %d", t.label, spinner[t.frame%len(spinner)], t.done)
		t.frame++
	}
	line := head
	if t.detail != "" {
		line = head + " " + truncate(t.detail, t.width-utf8.RuneCountInString(head)-1)
	}
	_, _ = io.WriteString(t.w, clearLine+line)
}

// truncate shortens s to at most max runes, ending in an ellipsis when cut.
func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	if max == 1 {
		return ellipsis
	}
	r := []rune(s)
	return string(r[:max-1]) + ellipsis
}
