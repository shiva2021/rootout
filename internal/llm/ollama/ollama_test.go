package ollama

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const tagsJSON = `{"models":[
 {"name":"qwen2.5-coder:3b","model":"qwen2.5-coder:3b","modified_at":"2026-10-01T10:00:00.000000+01:00","size":1929912432,"digest":"f72c60cabf62","details":{"parent_model":"","format":"gguf","family":"qwen2","families":["qwen2"],"parameter_size":"3.1B","quantization_level":"Q4_K_M"}},
 {"name":"llama3.2:latest","model":"llama3.2:latest","modified_at":"2026-09-01T10:00:00.000000+01:00","size":2019393189,"digest":"a80c4f17acd5","details":{"parent_model":"","format":"gguf","family":"llama","families":["llama"],"parameter_size":"3.2B","quantization_level":"Q4_K_M"}},
 {"name":"qwen2.5-coder:32b","model":"qwen2.5-coder:32b","modified_at":"2026-09-01T10:00:00.000000+01:00","size":19851349669,"digest":"4bd6cbf2d094","details":{"parent_model":"","format":"gguf","family":"qwen2","families":["qwen2"],"parameter_size":"32.8B","quantization_level":"Q4_K_M"}}
]}`

func newServer(t *testing.T, pull string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var pulls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"version":"0.12.6"}`)
	})
	mux.HandleFunc("GET /api/tags", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, tagsJSON)
	})
	mux.HandleFunc("POST /api/pull", func(w http.ResponseWriter, r *http.Request) {
		pulls.Add(1)
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), `"stream":true`) || !strings.Contains(string(b), `"model":"qwen2.5-coder:3b"`) {
			http.Error(w, `{"error":"bad request body"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w, pull)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &pulls
}

func testClient(url string) *Client {
	c := New(url)
	c.Run = func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("exec not allowed in tests")
	}
	c.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	c.Stat = func(string) (os.FileInfo, error) { return nil, fs.ErrNotExist }
	return c
}

func TestDetectRunning(t *testing.T) {
	srv, _ := newServer(t, "")
	v, err := testClient(srv.URL).Detect(context.Background())
	if err != nil || v != "0.12.6" {
		t.Fatalf("Detect = %q, %v", v, err)
	}
}

func TestDetectDown(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing listens here now

	tests := []struct {
		name    string
		onPath  bool
		hasApp  bool
		wantErr error
	}{
		{"cli on PATH", true, false, ErrNotRunning},
		{"app only", false, true, ErrNotRunning},
		{"nothing", false, false, ErrNotInstalled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := testClient(url)
			c.LookPath = func(name string) (string, error) {
				if tt.onPath && name == "ollama" {
					return "/opt/homebrew/bin/ollama", nil
				}
				return "", errors.New("not found")
			}
			c.Stat = func(p string) (os.FileInfo, error) {
				if tt.hasApp && p == "/Applications/Ollama.app" {
					return nil, nil
				}
				return nil, fs.ErrNotExist
			}
			_, err := c.Detect(context.Background())
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestListInstalled(t *testing.T) {
	srv, _ := newServer(t, "")
	got, err := testClient(srv.URL).ListInstalled(context.Background(), Hardware{RAMBytes: 8 << 30, AppleSilicon: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d models", len(got))
	}
	want := Installed{Name: "qwen2.5-coder:3b", Family: "qwen2", ParameterSize: "3.1B", Quantization: "Q4_K_M", SizeBytes: 1929912432, FitsRAM: true}
	if got[0] != want {
		t.Errorf("got[0] = %+v, want %+v", got[0], want)
	}
	if got[2].FitsRAM {
		t.Errorf("32b on 8 GB should not fit: %+v", got[2])
	}
}

func TestPull(t *testing.T) {
	// A truncated stream is retried (Ollama resumes partial blobs); skip the
	// backoff and count only real progress lines.
	pullSleep = func(context.Context, time.Duration) error { return nil }
	t.Cleanup(func() { pullSleep = pullSleepDefault })
	const ok = `{"status":"pulling manifest"}
{"status":"pulling 5ee4f07cdb9b","digest":"sha256:5ee4f07cdb9b","total":1929912432}
{"status":"pulling 5ee4f07cdb9b","digest":"sha256:5ee4f07cdb9b","total":1929912432,"completed":964956216}
{"status":"pulling 5ee4f07cdb9b","digest":"sha256:5ee4f07cdb9b","total":1929912432,"completed":1929912432}
{"status":"verifying sha256 digest"}
{"status":"writing manifest"}
{"status":"success"}
`
	const failing = `{"status":"pulling manifest"}
{"status":"pulling 5ee4f07cdb9b","digest":"sha256:5ee4f07cdb9b","total":1929912432,"completed":1000}
{"error":"write /Users/x/.ollama/models/blobs: no space left on device"}
`
	const truncated = `{"status":"pulling manifest"}
`
	tests := []struct {
		name, stream, wantErr string
		wantCalls             int
	}{
		{"success", ok, "", 7},
		{"error line", failing, "no space left on device", 2},
		{"no success", truncated, "stream ended before success", PullRetries},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := newServer(t, tt.stream)
			var calls int
			var lastDone, lastTotal int64
			err := testClient(srv.URL).Pull(context.Background(), "qwen2.5-coder:3b", func(st string, c, tot int64) {
				if strings.HasPrefix(st, "retrying") {
					return
				}
				calls++
				if tot > 0 {
					lastDone, lastTotal = c, tot
				}
			})
			if tt.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
			if calls != tt.wantCalls {
				t.Errorf("progress calls = %d, want %d", calls, tt.wantCalls)
			}
			if tt.wantErr == "" && (lastDone != 1929912432 || lastTotal != 1929912432) {
				t.Errorf("last progress = %d/%d", lastDone, lastTotal)
			}
		})
	}
}

func TestPullRejectsBadName(t *testing.T) {
	srv, pulls := newServer(t, "")
	for _, name := range []string{"", "qwen; rm -rf /", "a b", "model$(x)", "x\n"} {
		if err := testClient(srv.URL).Pull(context.Background(), name, nil); err == nil {
			t.Errorf("Pull(%q) succeeded", name)
		}
	}
	if n := pulls.Load(); n != 0 {
		t.Fatalf("server saw %d pull requests", n)
	}
}

func TestStart(t *testing.T) {
	srv, _ := newServer(t, "")
	tests := []struct {
		name    string
		brew    bool
		formula bool
		app     bool
		wantCmd string
		wantErr error
	}{
		{"brew formula", true, true, true, "brew services start ollama", nil},
		{"app", true, false, true, "open -a Ollama", nil},
		{"neither", false, false, false, "", ErrNotInstalled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := testClient(srv.URL)
			c.pollEvery, c.startWait = time.Millisecond, 50*time.Millisecond
			c.LookPath = func(name string) (string, error) {
				if tt.brew && name == "brew" {
					return "/opt/homebrew/bin/brew", nil
				}
				return "", errors.New("not found")
			}
			c.Stat = func(string) (os.FileInfo, error) {
				if tt.app {
					return nil, nil
				}
				return nil, fs.ErrNotExist
			}
			var ran []string
			c.Run = func(_ context.Context, name string, args ...string) ([]byte, error) {
				cmd := strings.Join(append([]string{name}, args...), " ")
				if cmd == "brew list --formula ollama" {
					if tt.formula {
						return []byte("/opt/homebrew/Cellar/ollama/0.12.6/bin/ollama\n"), nil
					}
					return nil, errors.New("exit status 1")
				}
				ran = append(ran, cmd)
				return nil, nil
			}
			err := c.Start(context.Background())
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			got := strings.Join(ran, ";")
			if got != tt.wantCmd {
				t.Errorf("ran %q, want %q", got, tt.wantCmd)
			}
		})
	}
}

func TestInstallGuide(t *testing.T) {
	for _, hasBrew := range []bool{true, false} {
		g := InstallGuide(hasBrew)
		if len(g) != 2 || g[0].Runnable != hasBrew || g[1].Runnable {
			t.Errorf("InstallGuide(%v) = %+v", hasBrew, g)
		}
		if g[0].Command != "brew install ollama && brew services start ollama" {
			t.Errorf("brew command = %q", g[0].Command)
		}
	}
}

func TestProbe(t *testing.T) {
	tests := []struct {
		name string
		out  map[string]string // missing key → sysctl error
		want Hardware
	}{
		{"M1 8 GB", map[string]string{
			"hw.memsize": "8589934592\n", "machdep.cpu.brand_string": "Apple M1\n",
			"hw.optional.arm64": "1\n", "hw.ncpu": "8\n",
		}, Hardware{Chip: "Apple M1", AppleSilicon: true, RAMBytes: 8 << 30, Cores: 8}},
		{"Intel 16 GB", map[string]string{
			"hw.memsize": "17179869184\n", "machdep.cpu.brand_string": "Intel(R) Core(TM) i7-9750H CPU @ 2.60GHz\n",
			"hw.ncpu": "12\n",
		}, Hardware{Chip: "Intel(R) Core(TM) i7-9750H CPU @ 2.60GHz", AppleSilicon: false, RAMBytes: 16 << 30, Cores: 12}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name != "sysctl" || len(args) != 2 || args[0] != "-n" {
					return nil, fmt.Errorf("unexpected command %s %v", name, args)
				}
				if v, ok := tt.out[args[1]]; ok {
					return []byte(v), nil
				}
				return []byte("sysctl: unknown oid '" + args[1] + "'"), errors.New("exit status 1")
			}
			got, err := Probe(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("Probe = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// Ollama aborts stalled downloads but keeps partial blobs; Pull must retry
// those and resume, and must not retry a missing model.
func TestPullRetriesStalledDownloads(t *testing.T) {
	pullSleep = func(context.Context, time.Duration) error { return nil }
	t.Cleanup(func() { pullSleep = pullSleepDefault })
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			_, _ = w.Write([]byte(`{"status":"pulling abc","total":100,"completed":10}` + "\n" + `{"error":"max retries exceeded: download too slow"}` + "\n"))
			return
		}
		_, _ = w.Write([]byte(`{"status":"pulling abc","total":100,"completed":100}` + "\n" + `{"status":"success"}` + "\n"))
	}))
	defer srv.Close()
	var statuses []string
	err := testClient(srv.URL).Pull(context.Background(), "qwen3.5:4b", func(s string, _, _ int64) { statuses = append(statuses, s) })
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3 (two stalls, then success)", calls)
	}
	joined := strings.Join(statuses, "|")
	if !strings.Contains(joined, "retrying (1/") || !strings.Contains(joined, "download too slow") || !strings.Contains(joined, "success") {
		t.Fatalf("statuses = %q", joined)
	}

	calls = 0
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"error":"pull model manifest: file does not exist"}` + "\n"))
	}))
	defer srv2.Close()
	if err := testClient(srv2.URL).Pull(context.Background(), "nope:1b", nil); err == nil || calls != 1 {
		t.Fatalf("missing model: err=%v calls=%d, want one attempt", err, calls)
	}

	calls = 0
	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"error":"max retries exceeded: download too slow"}` + "\n"))
	}))
	defer srv3.Close()
	err = testClient(srv3.URL).Pull(context.Background(), "qwen3.5:4b", nil)
	if err == nil || calls != PullRetries || !strings.Contains(err.Error(), "gave up after") {
		t.Fatalf("persistent stall: err=%v calls=%d", err, calls)
	}
}
