package tui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/shiva2021/purge/internal/llm/ollama"
)

var testHW = ollama.Hardware{Chip: "Apple M1", AppleSilicon: true, RAMBytes: 16 << 30, Cores: 8}

func testInstalled() []ollama.Installed {
	return []ollama.Installed{
		{Name: "llama3.2:3b", ParameterSize: "3.2B", Quantization: "Q4_K_M", SizeBytes: 2_000_000_000, FitsRAM: true},
		{Name: "qwen2.5-coder:7b", ParameterSize: "7.6B", Quantization: "Q4_K_M", SizeBytes: 4_700_000_000, FitsRAM: true},
	}
}

// blockingPull is an Ollama stand-in whose /api/pull hangs until the
// request is cancelled, so tests can feed pull messages by hand.
func blockingPull(t *testing.T) *ollama.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	return ollama.New(srv.URL)
}

func newReadyPicker(t *testing.T, current string) *ModelPicker {
	t.Helper()
	m := NewModelPicker(context.Background(), ModelDeps{Client: blockingPull(t), Hardware: testHW}, current)
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m.Update(detectedMsg{})
	if m.state != stateListing {
		t.Fatalf("after detect: state %d", m.state)
	}
	m.Update(listedMsg{installed: testInstalled()})
	return m
}

func pressM(m *ModelPicker, keys ...tea.KeyPressMsg) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		_, cmd = m.Update(k)
	}
	return cmd
}

func viewM(m *ModelPicker) string { return plain(m.View().Content) }

// cursorTo moves the cursor to the row whose rendered text contains s.
func cursorTo(t *testing.T, m *ModelPicker, s string) {
	t.Helper()
	for range len(m.rows) {
		if strings.Contains(plain(m.renderRow(m.cursor)), s) {
			return
		}
		pressM(m, keyDown)
	}
	for range len(m.rows) {
		if strings.Contains(plain(m.renderRow(m.cursor)), s) {
			return
		}
		pressM(m, keyUp)
	}
	t.Fatalf("no selectable row contains %q", s)
}

func TestModelPickerReadyView(t *testing.T) {
	m := newReadyPicker(t, "qwen2.5-coder:7b")
	out := viewM(m)
	for _, want := range []string{
		"Installed", "llama3.2:3b", "3.2B", "Q4_K_M", "2.0 GB", "fits",
		"qwen2.5-coder:7b (current)",
		"Recommended for this Mac — Apple M1, 16 GB",
		"qwen3.5:9b", "7.6 GB", "comfortable", "too large", "installed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q\n%s", want, out)
		}
	}
	if r := m.rows[m.cursor]; r.kind != mrInstalled || m.installed[r.idx].Name != "qwen2.5-coder:7b" {
		t.Fatalf("current model not pre-selected: %+v", r)
	}
}

func TestModelPickerEnterInstalled(t *testing.T) {
	m := newReadyPicker(t, "qwen2.5-coder:7b")
	if cmd := pressM(m, keyEnter); cmd == nil {
		t.Fatal("enter should quit")
	}
	if name, ok := m.Chosen(); !ok || name != "qwen2.5-coder:7b" {
		t.Fatalf("chosen = %q %v", name, ok)
	}
}

func TestModelPickerTooLargeNotSelectable(t *testing.T) {
	m := newReadyPicker(t, "")
	cursorTo(t, m, "qwen3-coder:30b")
	if cmd := pressM(m, keyEnter); cmd != nil {
		t.Fatal("enter on a too-large model returned a Cmd")
	}
	if _, ok := m.Chosen(); ok || m.ask != "" || m.state != stateReady {
		t.Fatal("too-large model was selectable")
	}
	if !strings.Contains(viewM(m), "this Mac has 16 GB") {
		t.Fatalf("no flash explaining why:\n%s", viewM(m))
	}
}

func TestModelPickerDownload(t *testing.T) {
	m := newReadyPicker(t, "")
	cursorTo(t, m, "qwen3.5:9b")
	pressM(m, keyEnter)
	if !strings.Contains(viewM(m), "Download qwen3.5:9b (7.6 GB)? y/n") {
		t.Fatalf("no download confirm:\n%s", viewM(m))
	}
	if cmd := pressM(m, tea.KeyPressMsg{Code: 'y', Text: "y"}); cmd == nil {
		t.Fatal("y should start the pull")
	}
	if m.state != statePulling {
		t.Fatalf("state = %d, want pulling", m.state)
	}
	m.Update(pullProgressMsg{id: m.pullID, status: "pulling abc", completed: 50, total: 100})
	out := viewM(m)
	if !strings.Contains(out, " 50%") || !strings.Contains(out, "pulling abc") || !strings.Contains(out, "Downloading qwen3.5:9b") {
		t.Fatalf("progress not shown:\n%s", out)
	}
	m.Update(pullProgressMsg{id: m.pullID - 1, status: "stale", completed: 90, total: 100})
	if strings.Contains(viewM(m), "stale") {
		t.Fatal("stale pull message applied")
	}
	if _, cmd := m.Update(pullDoneMsg{id: m.pullID}); cmd == nil {
		t.Fatal("success should quit")
	}
	if name, ok := m.Chosen(); !ok || name != "qwen3.5:9b" {
		t.Fatalf("chosen = %q %v", name, ok)
	}
}

func TestModelPickerPullErrorAndCancel(t *testing.T) {
	m := newReadyPicker(t, "")
	cursorTo(t, m, "qwen3.5:9b")
	pressM(m, keyEnter, tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.Update(pullDoneMsg{id: m.pullID, err: errors.New("ollama: pull qwen3.5:9b: disk full")})
	if m.state != stateReady || !strings.Contains(viewM(m), "disk full") {
		t.Fatalf("error not shown:\n%s", viewM(m))
	}

	pressM(m, keyEnter, tea.KeyPressMsg{Code: 'y', Text: "y"})
	pressM(m, keyEsc)
	if m.state != stateReady || m.pullCancel != nil {
		t.Fatal("esc should cancel the pull and return to the list")
	}
	if _, ok := m.Chosen(); ok {
		t.Fatal("cancelled pull chose a model")
	}
}

func TestModelPickerNotInstalled(t *testing.T) {
	var ran bool
	d := ModelDeps{HasBrew: true, RunInstall: func(context.Context) error { ran = true; return nil }}
	m := NewModelPicker(context.Background(), d, "")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.Update(detectedMsg{err: ollama.ErrNotInstalled})
	out := viewM(m)
	for _, want := range []string{"not installed", "Homebrew", "brew install ollama && brew services start ollama", "Ollama.app", "(enter to run)"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q\n%s", want, out)
		}
	}
	pressM(m, keyEnter)
	if !strings.Contains(viewM(m), "Run `brew install ollama && brew services start ollama`? y/n") {
		t.Fatalf("no install confirm:\n%s", viewM(m))
	}
	for _, msg := range runCmd(pressM(m, tea.KeyPressMsg{Code: 'y', Text: "y"})) {
		if _, ok := msg.(installedOllamaMsg); ok {
			m.Update(msg)
		}
	}
	if !ran || m.state != stateDetecting {
		t.Fatalf("install ran=%v state=%d", ran, m.state)
	}
}

func TestModelPickerDetectAndListViaHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			_, _ = w.Write([]byte(`{"version":"0.12.0"}`))
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"llama3.2:3b","size":2000000000,"details":{"parameter_size":"3.2B","quantization_level":"Q4_K_M"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := ollama.New(srv.URL)
	m := NewModelPicker(context.Background(), ModelDeps{Client: c, Hardware: testHW}, "llama3.2:3b")
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	if !strings.Contains(viewM(m), "looking for Ollama") {
		t.Fatal("no detecting state")
	}
	m.Update(m.detect()())
	m.Update(m.list()())
	if m.state != stateReady || !strings.Contains(viewM(m), "llama3.2:3b (current)") {
		t.Fatalf("not ready:\n%s", viewM(m))
	}
}

func TestModelPickerNotRunning(t *testing.T) {
	m := NewModelPicker(context.Background(), ModelDeps{}, "")
	m.Update(detectedMsg{err: ollama.ErrNotRunning})
	if !strings.Contains(viewM(m), "Start Ollama") {
		t.Fatalf("no start offer:\n%s", viewM(m))
	}
	m.Update(startedMsg{err: errors.New("boom")})
	if !strings.Contains(viewM(m), "could not start Ollama: boom") {
		t.Fatalf("start error not shown:\n%s", viewM(m))
	}
	pressM(m, keyEsc)
	if _, ok := m.Chosen(); ok {
		t.Fatal("esc should cancel")
	}
}
