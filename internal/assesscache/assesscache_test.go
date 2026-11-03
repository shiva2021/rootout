package assesscache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/shiva2021/rootout/internal/ext"
	"github.com/shiva2021/rootout/internal/heur"
	"github.com/shiva2021/rootout/internal/llm"
	"github.com/shiva2021/rootout/internal/testutil"
)

func report() heur.Report {
	return heur.Report{
		Ext:      ext.Extension{Editor: "vscode", ID: "evil.stealer", Version: "1.0.0"},
		Score:    85,
		Findings: []heur.Finding{{Rule: "credential_exfil", Severity: heur.Critical, File: "out/b.js", Line: 10}},
	}
}

func fakeEnv(t *testing.T) (string, func(string) string) {
	t.Helper()
	env := testutil.FakeHome(t, testutil.Spec{})
	return Path(env, func(string) string { return "" }), func(string) string { return "" }
}

func TestPathDefaultAndEnv(t *testing.T) {
	env := testutil.FakeHome(t, testutil.Spec{})
	if got, want := Path(env, func(string) string { return "" }), env.User(".cache", "rootout", "assessments.json"); got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
	getenv := func(name string) string {
		if name == EnvVar {
			return "/elsewhere/a.json"
		}
		return ""
	}
	if got := Path(env, getenv); got != "/elsewhere/a.json" {
		t.Errorf("Path with %s = %q", EnvVar, got)
	}
}

func TestOpenMissingIsEmpty(t *testing.T) {
	path, _ := fakeEnv(t)
	c, err := Open(path)
	if err != nil || c.Len() != 0 {
		t.Fatalf("Open missing: %v, len %d", err, c.Len())
	}
	if _, ok := c.Lookup(IDFor(report(), "anthropic", "claude-x")); ok {
		t.Fatal("empty cache should miss")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("Open must not create the file")
	}
}

func TestStoreLookupRoundTrip(t *testing.T) {
	path, _ := fakeEnv(t)
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	id := IDFor(report(), "anthropic", "claude-x")
	a := llm.Assessment{Verdict: llm.Malicious, Confidence: 0.9, Reasoning: "Steals keys.", IOCs: []string{"evil.example"}, Provider: "anthropic", Model: "claude-x"}
	if err := c.Store(id, a); err != nil {
		t.Fatal(err)
	}
	got, ok := c.Lookup(id)
	if !ok || !got.Cached || got.Verdict != llm.Malicious || got.Reasoning != "Steals keys." || got.IOCs[0] != "evil.example" {
		t.Fatalf("Lookup = %+v, %v", got, ok)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode %o, want 600", info.Mode().Perm())
	}
	if dir, _ := os.Stat(filepath.Dir(path)); dir.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %o, want 700", dir.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("temp file left behind: %v", entries)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), `"cached":true`) {
		t.Error("Cached must not be written to disk")
	}
	var file struct {
		Version int              `json:"version"`
		Entries map[string]Entry `json:"entries"`
	}
	if err := json.Unmarshal(raw, &file); err != nil || file.Version != fileVersion || len(file.Entries) != 1 {
		t.Fatalf("file = %s (%v)", raw, err)
	}
	for _, e := range file.Entries {
		if e.At.IsZero() {
			t.Error("entry time not set")
		}
	}
	// A fresh Cache over the same file sees the entry.
	c2, err := Open(path)
	if err != nil || c2.Len() != 1 {
		t.Fatalf("reopen: %v, len %d", err, c2.Len())
	}
	if _, ok := c2.Lookup(id); !ok {
		t.Fatal("reopened cache should hit")
	}
}

func TestIDIncludesEveryField(t *testing.T) {
	path, _ := fakeEnv(t)
	c, _ := Open(path)
	base := IDFor(report(), "anthropic", "claude-x")
	if err := c.Store(base, llm.Assessment{Verdict: llm.Benign}); err != nil {
		t.Fatal(err)
	}
	variants := map[string]EntryID{}
	for name, f := range map[string]func(*EntryID){
		"editor":        func(id *EntryID) { id.Editor = "cursor" },
		"ext":           func(id *EntryID) { id.ExtID = "other.ext" },
		"version":       func(id *EntryID) { id.Version = "1.0.1" },
		"provider":      func(id *EntryID) { id.Provider = "openai" },
		"model":         func(id *EntryID) { id.Model = "gpt-x" },
		"promptVersion": func(id *EntryID) { id.PromptVersion++ },
		"fingerprint":   func(id *EntryID) { id.Fingerprint = "0000000000000000" },
	} {
		id := base
		f(&id)
		variants[name] = id
	}
	for name, id := range variants {
		if _, ok := c.Lookup(id); ok {
			t.Errorf("changing %s should miss", name)
		}
	}
	// And the fingerprint tracks what the model saw.
	r := report()
	r.Score = 10
	if IDFor(r, "anthropic", "claude-x") == base {
		t.Error("a rescored report should have a different id")
	}
	if base.PromptVersion != llm.PromptVersion || base.Fingerprint != llm.Fingerprint(report()) {
		t.Errorf("id = %+v", base)
	}
}

func TestOpenCorruptFileIsEmpty(t *testing.T) {
	path, _ := fakeEnv(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"{not json", `{"version":99,"entries":{"x":{}}}`} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		c, err := Open(path)
		if err != nil || c.Len() != 0 {
			t.Fatalf("%q: Open = %v, len %d; want an empty cache", body, err, c.Len())
		}
		if err := c.Store(IDFor(report(), "anthropic", "m"), llm.Assessment{Verdict: llm.Benign}); err != nil {
			t.Fatal(err)
		}
		if c2, _ := Open(path); c2.Len() != 1 {
			t.Fatalf("%q: the file should be replaced by a valid one", body)
		}
	}
}

func TestClear(t *testing.T) {
	path, _ := fakeEnv(t)
	if removed, err := Clear(path); err != nil || removed {
		t.Fatalf("Clear absent = %v, %v; want false, nil", removed, err)
	}
	c, _ := Open(path)
	_ = c.Store(IDFor(report(), "anthropic", "m"), llm.Assessment{})
	if removed, err := Clear(path); err != nil || !removed {
		t.Fatalf("Clear present = %v, %v; want true, nil", removed, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file should be gone")
	}
}

func TestConcurrentStore(t *testing.T) {
	path, _ := fakeEnv(t)
	c, _ := Open(path)
	var wg sync.WaitGroup
	for i, model := range []string{"m1", "m2", "m3", "m4"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.Store(IDFor(report(), "anthropic", model), llm.Assessment{Confidence: float64(i)}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if c2, _ := Open(path); c2.Len() != 4 {
		t.Fatalf("len after concurrent stores = %d, want 4", c2.Len())
	}
}

func TestNeverContainsKeys(t *testing.T) {
	path, _ := fakeEnv(t)
	c, _ := Open(path)
	_ = c.Store(IDFor(report(), "anthropic", "m"), llm.Assessment{Verdict: llm.Benign, Reasoning: "ok", Provider: "anthropic", Model: "m"})
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "sk-") {
		t.Fatalf("file must never hold a key:\n%s", raw)
	}
}
