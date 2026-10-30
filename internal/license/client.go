package license

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ServiceURL is the key service origin baked into release builds.
const ServiceURL = "https://rootout-keys.CHANGEME.workers.dev" // Task 7 sets the real host

const maxBody = 64 << 10

// ErrTrialUsed means /trial refused because this Mac already had a trial.
var ErrTrialUsed = errors.New("license: the trial was already used on this Mac")

// ErrServiceUnavailable wraps network errors, timeouts, 429 and 5xx answers.
var ErrServiceUnavailable = errors.New("license: key service unavailable")

// DeniedError is a 403 from /renew: the membership is no longer active.
type DeniedError struct{ Reason string }

func (e *DeniedError) Error() string { return "membership check failed: " + e.Reason }

// Client talks to the key service.
type Client struct {
	BaseURL   string
	UserAgent string       // "rootout/<version>"
	HTTP      *http.Client // nil means a client with a 3 s timeout
}

func (c Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 3 * time.Second}
}

// Renew exchanges key for a fresh one. A 403 returns *DeniedError.
func (c Client) Renew(ctx context.Context, key string) (string, error) {
	status, body, err := c.post(ctx, "/renew", map[string]string{"key": key})
	if err != nil {
		return "", err
	}
	switch status {
	case http.StatusOK:
		return keyFrom(body)
	case http.StatusForbidden:
		var r struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(body, &r)
		if r.Reason == "" {
			r.Reason = "denied"
		}
		return "", &DeniedError{Reason: r.Reason}
	}
	return "", fmt.Errorf("%w: /renew answered %d", ErrServiceUnavailable, status)
}

// Trial asks for a 7-day trial key for machine id mid.
func (c Client) Trial(ctx context.Context, mid string) (string, error) {
	status, body, err := c.post(ctx, "/trial", map[string]string{"mid": mid})
	if err != nil {
		return "", err
	}
	switch status {
	case http.StatusOK:
		return keyFrom(body)
	case http.StatusConflict:
		return "", ErrTrialUsed
	}
	return "", fmt.Errorf("%w: /trial answered %d", ErrServiceUnavailable, status)
}

func keyFrom(body []byte) (string, error) {
	var r struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(body, &r); err != nil || r.Key == "" {
		return "", fmt.Errorf("%w: malformed response", ErrServiceUnavailable)
	}
	return r.Key, nil
}

// post sends in as JSON and returns the status and at most maxBody bytes of the
// body. Transport failures wrap ErrServiceUnavailable.
func (c Client) post(ctx context.Context, path string, in map[string]string) (int, []byte, error) {
	payload, err := json.Marshal(in)
	if err != nil {
		return 0, nil, fmt.Errorf("license: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, fmt.Errorf("license: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %w", ErrServiceUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return 0, nil, fmt.Errorf("%w: read response: %w", ErrServiceUnavailable, err)
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return 0, nil, fmt.Errorf("%w: %s answered %d", ErrServiceUnavailable, path, resp.StatusCode)
	}
	return resp.StatusCode, body, nil
}
