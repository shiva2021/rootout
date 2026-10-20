package progress

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newTerm(t *testing.T, opts ...Option) (*bytes.Buffer, *fakeClock, Reporter) {
	t.Helper()
	var buf bytes.Buffer
	clk := &fakeClock{t: time.Unix(1_000_000, 0)}
	opts = append([]Option{ForceTTY(), Width(80), Clock(clk.now)}, opts...)
	return &buf, clk, Term(&buf, opts...)
}

func TestNop(t *testing.T) {
	r := Nop()
	r.Step("before start")
	r.Start("x", 2)
	r.Step("a")
	r.Done()
	r.Done()
}

func TestRecorder(t *testing.T) {
	var r Recorder
	r.Start("scan", 2)
	r.Step("a")
	r.Step("b")
	r.Done()
	if r.Label != "scan" || r.Total != 2 || !r.Finished {
		t.Fatalf("got label=%q total=%d finished=%v", r.Label, r.Total, r.Finished)
	}
	if got := strings.Join(r.Steps, ","); got != "a,b" {
		t.Fatalf("steps = %q", got)
	}
}

func TestTermNotTTYWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	r := Term(&buf)
	r.Start("scan", 3)
	r.Step("a")
	r.Done()
	if buf.Len() != 0 {
		t.Fatalf("wrote %q to a non-terminal", buf.String())
	}
}

func TestTermBar(t *testing.T) {
	buf, clk, r := newTerm(t)

	r.Start("scan", 3)
	first := buf.String()
	if !strings.HasPrefix(first, clearLine+"scan ") || !strings.Contains(first, " 0/3") {
		t.Fatalf("start render = %q", first)
	}
	if !strings.Contains(first, strings.Repeat("▱", 20)) {
		t.Fatalf("start bar not empty: %q", first)
	}

	buf.Reset()
	r.Step("a")
	if buf.Len() != 0 {
		t.Fatalf("step within throttle re-rendered: %q", buf.String())
	}

	clk.advance(150 * time.Millisecond)
	r.Step("b")
	got := buf.String()
	want := clearLine + "scan " + strings.Repeat("▰", 13) + strings.Repeat("▱", 7) + " 2/3 b"
	if got != want {
		t.Fatalf("step render\n got %q\nwant %q", got, want)
	}

	buf.Reset()
	r.Step("c") // final state renders despite throttle
	if !strings.Contains(buf.String(), strings.Repeat("▰", 20)+" 3/3 c") {
		t.Fatalf("final render = %q", buf.String())
	}

	buf.Reset()
	r.Done()
	if buf.String() != clearLine {
		t.Fatalf("done wrote %q", buf.String())
	}
	buf.Reset()
	r.Step("late")
	r.Done()
	if buf.Len() != 0 {
		t.Fatalf("wrote after Done: %q", buf.String())
	}
}

func TestTermStepBeforeStart(t *testing.T) {
	buf, _, r := newTerm(t)
	r.Step("early")
	r.Done()
	if buf.Len() != 0 {
		t.Fatalf("wrote before Start: %q", buf.String())
	}
}

func TestTermSpinner(t *testing.T) {
	buf, clk, r := newTerm(t)
	r.Start("scan", 0)
	frames := []string{buf.String()}
	for i := 1; i < 4; i++ {
		buf.Reset()
		clk.advance(throttle)
		r.Step("")
		frames = append(frames, buf.String())
	}
	for i, want := range spinner {
		if !strings.HasPrefix(frames[i], clearLine+"scan "+want+" ") {
			t.Errorf("frame %d = %q, want spinner %q", i, frames[i], want)
		}
	}
	if !strings.HasSuffix(frames[3], " 3") {
		t.Errorf("spinner count: %q", frames[3])
	}
	if strings.Contains(frames[3], "▰") || strings.Contains(frames[3], "▱") {
		t.Errorf("spinner mode rendered a bar: %q", frames[3])
	}
}

func TestTermTruncate(t *testing.T) {
	buf, clk, r := newTerm(t, Width(40))
	r.Start("scan", 2)
	buf.Reset()
	clk.advance(throttle)
	detail := strings.Repeat("日本語ファイル", 10)
	r.Step(detail)
	line := strings.TrimPrefix(buf.String(), clearLine)
	if n := len([]rune(line)); n != 40 {
		t.Fatalf("line is %d runes, want 40: %q", n, line)
	}
	if !strings.HasSuffix(line, ellipsis) {
		t.Fatalf("missing ellipsis: %q", line)
	}
	if !strings.HasPrefix(line, "scan "+strings.Repeat("▰", 10)+strings.Repeat("▱", 10)+" 1/2 日本語") {
		t.Fatalf("unexpected prefix: %q", line)
	}
}

func TestTermShortDetailNotTruncated(t *testing.T) {
	if got := truncate("abc", 3); got != "abc" {
		t.Fatalf("truncate = %q", got)
	}
	if got := truncate("abcd", 1); got != ellipsis {
		t.Fatalf("truncate(1) = %q", got)
	}
	if got := truncate("abcd", 0); got != "" {
		t.Fatalf("truncate(0) = %q", got)
	}
}

func TestTermConcurrentSteps(t *testing.T) {
	const workers, per = 8, 25
	buf, clk, r := newTerm(t)
	r.Start("scan", workers*per)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < per; i++ {
				clk.advance(throttle)
				r.Step("item")
			}
		}()
	}
	wg.Wait()
	tr := r.(*termReporter)
	tr.mu.Lock()
	done := tr.done
	tr.mu.Unlock()
	if done != workers*per {
		t.Fatalf("done = %d, want %d", done, workers*per)
	}
	if !strings.Contains(buf.String(), "200/200") {
		t.Fatalf("final render missing: %q", lastLine(buf.String()))
	}
}

func lastLine(s string) string {
	parts := strings.Split(s, clearLine)
	return parts[len(parts)-1]
}
