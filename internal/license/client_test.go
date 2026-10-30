package license

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// serve starts a test server that checks the request and answers with status and body.
func serve(t *testing.T, path, wantField string, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != path {
			t.Errorf("request %s %s, want POST %s", r.Method, r.URL.Path, path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		if ua := r.Header.Get("User-Agent"); ua != "rootout/test" {
			t.Errorf("User-Agent = %q", ua)
		}
		var in map[string]string
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in[wantField] == "" {
			t.Errorf("body %v (%v), want field %q", in, err, wantField)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func client(srv *httptest.Server) Client {
	return Client{BaseURL: srv.URL, UserAgent: "rootout/test", HTTP: srv.Client()}
}

func TestClientRenewOK(t *testing.T) {
	srv := serve(t, "/renew", "key", http.StatusOK, `{"key":"ro1.new.key"}`)
	got, err := client(srv).Renew(context.Background(), "ro1.old.key")
	if err != nil || got != "ro1.new.key" {
		t.Errorf("Renew = %q, %v", got, err)
	}
}

func TestClientRenewDenied(t *testing.T) {
	srv := serve(t, "/renew", "key", http.StatusForbidden, `{"reason":"declined_patron"}`)
	_, err := client(srv).Renew(context.Background(), "ro1.old.key")
	var denied *DeniedError
	if !errors.As(err, &denied) || denied.Reason != "declined_patron" {
		t.Fatalf("err = %v, want *DeniedError{declined_patron}", err)
	}
	if err.Error() != "membership check failed: declined_patron" {
		t.Errorf("Error() = %q", err.Error())
	}
}

func TestClientRenew503(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusTooManyRequests, http.StatusInternalServerError} {
		srv := serve(t, "/renew", "key", status, `oops`)
		if _, err := client(srv).Renew(context.Background(), "ro1.old.key"); !errors.Is(err, ErrServiceUnavailable) {
			t.Errorf("status %d: err = %v, want ErrServiceUnavailable", status, err)
		}
	}
}

func TestClientRenewBadBody(t *testing.T) {
	for name, body := range map[string]string{"not json": "nope", "empty key": `{"key":""}`} {
		srv := serve(t, "/renew", "key", http.StatusOK, body)
		if _, err := client(srv).Renew(context.Background(), "ro1.old.key"); !errors.Is(err, ErrServiceUnavailable) {
			t.Errorf("%s: err = %v, want ErrServiceUnavailable", name, err)
		}
	}
}

func TestClientRenewUnexpectedStatus(t *testing.T) {
	srv := serve(t, "/renew", "key", http.StatusBadRequest, `{}`)
	if _, err := client(srv).Renew(context.Background(), "ro1.old.key"); !errors.Is(err, ErrServiceUnavailable) {
		t.Errorf("err = %v, want ErrServiceUnavailable", err)
	}
}

func TestClientTrialOK(t *testing.T) {
	srv := serve(t, "/trial", "mid", http.StatusOK, `{"key":"ro1.trial.key"}`)
	got, err := client(srv).Trial(context.Background(), "0123456789abcdef0123456789abcdef")
	if err != nil || got != "ro1.trial.key" {
		t.Errorf("Trial = %q, %v", got, err)
	}
}

func TestClientTrialUsed(t *testing.T) {
	srv := serve(t, "/trial", "mid", http.StatusConflict, `{"reason":"trial_used"}`)
	if _, err := client(srv).Trial(context.Background(), "0123456789abcdef0123456789abcdef"); !errors.Is(err, ErrTrialUsed) {
		t.Errorf("err = %v, want ErrTrialUsed", err)
	}
}

func TestClientTrialOtherStatus(t *testing.T) {
	srv := serve(t, "/trial", "mid", http.StatusForbidden, `{}`)
	if _, err := client(srv).Trial(context.Background(), "0123456789abcdef0123456789abcdef"); !errors.Is(err, ErrServiceUnavailable) {
		t.Errorf("err = %v, want ErrServiceUnavailable", err)
	}
}

func TestClientNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	c := client(srv)
	srv.Close()
	if _, err := c.Renew(context.Background(), "ro1.old.key"); !errors.Is(err, ErrServiceUnavailable) {
		t.Errorf("Renew err = %v, want ErrServiceUnavailable", err)
	}
	if _, err := c.Trial(context.Background(), "0123456789abcdef0123456789abcdef"); !errors.Is(err, ErrServiceUnavailable) {
		t.Errorf("Trial err = %v, want ErrServiceUnavailable", err)
	}
}

func TestClientTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(5 * time.Second):
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	c := Client{BaseURL: srv.URL, HTTP: &http.Client{Timeout: 100 * time.Millisecond}}
	start := time.Now()
	_, err := c.Renew(context.Background(), "ro1.old.key")
	if !errors.Is(err, ErrServiceUnavailable) {
		t.Errorf("err = %v, want ErrServiceUnavailable", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %v, want under 1s", d)
	}
}

func TestClientDefaultHTTP(t *testing.T) {
	srv := serve(t, "/renew", "key", http.StatusOK, `{"key":"ro1.new.key"}`)
	c := Client{BaseURL: srv.URL, UserAgent: "rootout/test"}
	if got, err := c.Renew(context.Background(), "ro1.old.key"); err != nil || got != "ro1.new.key" {
		t.Errorf("Renew = %q, %v", got, err)
	}
	if c.httpClient().Timeout != 3*time.Second {
		t.Errorf("default timeout = %v", c.httpClient().Timeout)
	}
}

func TestClientLargeBodyLimited(t *testing.T) {
	big := `{"key":"` + strings.Repeat("a", 70<<10) + `"}`
	srv := serve(t, "/renew", "key", http.StatusOK, big)
	if _, err := client(srv).Renew(context.Background(), "ro1.old.key"); !errors.Is(err, ErrServiceUnavailable) {
		t.Errorf("err = %v, want ErrServiceUnavailable for an oversized body", err)
	}
}
