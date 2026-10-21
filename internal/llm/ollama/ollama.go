// Package ollama talks to a local Ollama server: detect it, start it, list
// installed models, recommend models that fit this Mac, and pull one with
// progress (decision D-009). API: https://docs.ollama.com/api
package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/shiva2021/rootout/internal/run"
)

// DefaultURL is where Ollama listens unless configured otherwise.
const DefaultURL = "http://localhost:11434"

const appPath = "/Applications/Ollama.app"

var (
	ErrNotRunning    = errors.New("ollama: installed but not running")
	ErrNotInstalled  = errors.New("ollama: not installed")
	ErrModelNotFound = errors.New("ollama: model not found")
)

var validName = regexp.MustCompile(`^[a-zA-Z0-9._:/-]+$`)

// Installed is a model already present in the local Ollama store.
type Installed struct {
	Name, Family, ParameterSize, Quantization string
	SizeBytes                                 int64
	FitsRAM                                   bool
}

// Client is an Ollama API client. Every side effect (HTTP, exec, PATH and
// filesystem lookups) is a field so tests replace it.
type Client struct {
	BaseURL  string
	HTTP     *http.Client
	Run      run.Runner
	LookPath func(string) (string, error)
	Stat     func(string) (os.FileInfo, error)

	pollEvery, startWait time.Duration // Start's polling; zero means defaults
}

// New returns a client for baseURL (DefaultURL when empty) using the real
// HTTP client, runner, PATH and filesystem.
func New(baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultURL
	}
	return &Client{
		BaseURL:  strings.TrimRight(baseURL, "/"),
		HTTP:     &http.Client{},
		Run:      run.Default,
		LookPath: exec.LookPath,
		Stat:     os.Stat,
	}
}

// Detect returns the server version. When the server cannot be reached it
// returns ErrNotRunning if Ollama is installed (on PATH or as the app) and
// ErrNotInstalled otherwise.
func (c *Client) Detect(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var v struct {
		Version string `json:"version"`
	}
	err := c.getJSON(ctx, "/api/version", &v)
	if err == nil {
		return v.Version, nil
	}
	var unreachable *unreachableError
	if !errors.As(err, &unreachable) {
		return "", err
	}
	if _, lerr := c.LookPath("ollama"); lerr == nil {
		return "", ErrNotRunning
	}
	if _, serr := c.Stat(appPath); serr == nil {
		return "", ErrNotRunning
	}
	return "", ErrNotInstalled
}

// ListInstalled returns the models in the local store, each marked with
// whether it fits hw's RAM.
func (c *Client) ListInstalled(ctx context.Context, hw Hardware) ([]Installed, error) {
	var resp struct {
		Models []struct {
			Name    string `json:"name"`
			Size    int64  `json:"size"`
			Details struct {
				Family            string `json:"family"`
				ParameterSize     string `json:"parameter_size"`
				QuantizationLevel string `json:"quantization_level"`
			} `json:"details"`
		} `json:"models"`
	}
	if err := c.getJSON(ctx, "/api/tags", &resp); err != nil {
		return nil, err
	}
	out := make([]Installed, 0, len(resp.Models))
	for _, m := range resp.Models {
		out = append(out, Installed{
			Name:          m.Name,
			Family:        m.Details.Family,
			ParameterSize: m.Details.ParameterSize,
			Quantization:  m.Details.QuantizationLevel,
			SizeBytes:     m.Size,
			FitsRAM:       Fit(m.Size, hw.RAMBytes) != FitTooLarge,
		})
	}
	return out, nil
}

// Pull downloads model, calling progress for each streamed status line.
// completed and total are zero for lines without byte counts.
// Pull retry policy. Ollama aborts a download when the transfer stalls
// ("max retries exceeded: download too slow") but keeps the partial blobs,
// so a new request resumes where it stopped. Pull therefore retries
// transient failures with a short backoff; a bad model name or a registry
// 404 is not retried.
const (
	PullRetries    = 6
	pullBackoffMax = 15 * time.Second
)

var pullTransient = []string{
	"download too slow", "max retries exceeded", "stream ended before success", "read stream",
	"connection reset", "unexpected eof", "eof", "timeout", "tls handshake", "no such host",
	"temporarily unavailable", "i/o timeout", "502", "503", "504",
}

// pullSleep is a test seam.
var pullSleep = pullSleepDefault

func pullSleepDefault(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func pullIsTransient(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, t := range pullTransient {
		if strings.Contains(msg, t) {
			return true
		}
	}
	return false
}

// Pull downloads model, reporting progress, and retries stalled downloads
// (see PullRetries). The progress callback sees a status line
// "retrying (n/N): <reason>" before each new attempt.
func (c *Client) Pull(ctx context.Context, model string, progress func(status string, completed, total int64)) error {
	if !validName.MatchString(model) {
		return fmt.Errorf("ollama: invalid model name %q", model)
	}
	var err error
	for attempt := 1; attempt <= PullRetries; attempt++ {
		err = c.pullOnce(ctx, model, progress)
		if err == nil || ctx.Err() != nil || !pullIsTransient(err) {
			return err
		}
		if attempt == PullRetries {
			break
		}
		wait := min(pullBackoffMax, time.Duration(1<<attempt)*time.Second)
		if progress != nil {
			progress(fmt.Sprintf("retrying (%d/%d) in %s: %s", attempt, PullRetries-1, wait, pullReason(err)), 0, 0)
		}
		if serr := pullSleep(ctx, wait); serr != nil {
			return err
		}
	}
	return fmt.Errorf("%w (gave up after %d attempts: the connection to the Ollama registry keeps stalling; try again later, check the network, or pick a smaller model)", err, PullRetries)
}

// pullReason strips the "ollama: pull <model>: " prefix for status lines.
func pullReason(err error) string {
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 && strings.HasPrefix(msg, "ollama: pull ") {
		if j := strings.Index(msg[len("ollama: pull "):], ": "); j >= 0 {
			return msg[len("ollama: pull ")+j+2:]
		}
	}
	return msg
}

func (c *Client) pullOnce(ctx context.Context, model string, progress func(status string, completed, total int64)) error {
	body, err := json.Marshal(map[string]any{"model": model, "stream": true})
	if err != nil {
		return fmt.Errorf("ollama: pull %s: %w", model, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("ollama: pull %s: %w", model, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("ollama: pull %s: %w", model, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama: pull %s: %w", model, statusError(resp))
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev struct {
			Status    string `json:"status"`
			Total     int64  `json:"total"`
			Completed int64  `json:"completed"`
			Error     string `json:"error"`
		}
		if err := json.Unmarshal(line, &ev); err != nil {
			return fmt.Errorf("ollama: pull %s: decode %q: %w", model, line, err)
		}
		if ev.Error != "" {
			return fmt.Errorf("ollama: pull %s: %s", model, ev.Error)
		}
		if progress != nil {
			progress(ev.Status, ev.Completed, ev.Total)
		}
		if ev.Status == "success" {
			return nil
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("ollama: pull %s: read stream: %w", model, err)
	}
	return fmt.Errorf("ollama: pull %s: stream ended before success", model)
}

// Start launches an installed but stopped Ollama: via `brew services` when
// it is the Homebrew formula, else by opening Ollama.app. It then waits up
// to 15 s for the server to answer.
func (c *Client) Start(ctx context.Context) error {
	switch {
	case c.isBrewFormula(ctx):
		if out, err := c.Run(ctx, "brew", "services", "start", "ollama"); err != nil {
			return fmt.Errorf("ollama: brew services start: %w: %s", err, bytes.TrimSpace(out))
		}
	case c.hasApp():
		if out, err := c.Run(ctx, "open", "-a", "Ollama"); err != nil {
			return fmt.Errorf("ollama: open -a Ollama: %w: %s", err, bytes.TrimSpace(out))
		}
	default:
		return ErrNotInstalled
	}

	every, wait := c.pollEvery, c.startWait
	if every == 0 {
		every = 500 * time.Millisecond
	}
	if wait == 0 {
		wait = 15 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		_, err := c.Detect(ctx)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("ollama: started but not answering after %s: %w", wait, err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("ollama: start: %w", ctx.Err())
		case <-time.After(every):
		}
	}
}

func (c *Client) isBrewFormula(ctx context.Context) bool {
	if _, err := c.LookPath("brew"); err != nil {
		return false
	}
	_, err := c.Run(ctx, "brew", "list", "--formula", "ollama")
	return err == nil
}

func (c *Client) hasApp() bool {
	_, err := c.Stat(appPath)
	return err == nil
}

// InstallRoute is one official way to install Ollama.
type InstallRoute struct {
	Label, Command string
	Runnable       bool // rootout may run Command after the user confirms
}

// InstallGuide lists the official install routes. rootout never installs
// anything without confirmation.
func InstallGuide(hasBrew bool) []InstallRoute {
	return []InstallRoute{
		{Label: "Homebrew", Command: "brew install ollama && brew services start ollama", Runnable: hasBrew},
		{Label: "Ollama.app", Command: "Download Ollama.app from https://ollama.com/download", Runnable: false},
	}
}

// unreachableError marks a transport failure (no server answering).
type unreachableError struct{ err error }

func (e *unreachableError) Error() string { return e.err.Error() }
func (e *unreachableError) Unwrap() error { return e.err }

func (c *Client) getJSON(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return fmt.Errorf("ollama: GET %s: %w", path, err)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("ollama: GET %s: %w", path, &unreachableError{err})
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama: GET %s: %w", path, statusError(resp))
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("ollama: GET %s: decode: %w", path, err)
	}
	return nil
}

// Delete removes model from the local store (DELETE /api/delete). A model
// the server does not have yields ErrModelNotFound.
func (c *Client) Delete(ctx context.Context, model string) error {
	if !validName.MatchString(model) {
		return fmt.Errorf("ollama: delete: invalid model name %q", model)
	}
	body, err := json.Marshal(map[string]string{"model": model})
	if err != nil {
		return fmt.Errorf("ollama: delete %s: %w", model, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.BaseURL+"/api/delete", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("ollama: delete %s: %w", model, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("ollama: delete %s: %w", model, &unreachableError{err})
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrModelNotFound, model)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return fmt.Errorf("ollama: delete %s: %w", model, statusError(resp))
	}
	return nil
}

// statusError turns a non-200 response into an error, using Ollama's
// {"error": "..."} body when present.
func statusError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error != "" {
		return fmt.Errorf("%s: %s", resp.Status, e.Error)
	}
	return fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(b))
}
