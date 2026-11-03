package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const releaseJSON = `{"tag_name":"v0.8.0","html_url":"https://github.com/shiva2021/rootout/releases/tag/v0.8.0","published_at":"2026-11-05T01:02:03Z","body":"notes"}`

func newServer(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New("v0.7.0")
	c.URL = srv.URL
	return c
}

func TestLatest(t *testing.T) {
	var gotUA, gotAccept string
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotUA, gotAccept = r.Header.Get("User-Agent"), r.Header.Get("Accept")
		_, _ = w.Write([]byte(releaseJSON))
	})
	r, err := c.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := Release{Tag: "v0.8.0", URL: "https://github.com/shiva2021/rootout/releases/tag/v0.8.0", PublishedAt: time.Date(2026, 11, 5, 1, 2, 3, 0, time.UTC)}
	if r != want {
		t.Errorf("Latest = %+v, want %+v", r, want)
	}
	if gotUA != "rootout/v0.7.0" {
		t.Errorf("User-Agent = %q, want rootout/v0.7.0", gotUA)
	}
	if gotAccept != "application/vnd.github+json" {
		t.Errorf("Accept = %q", gotAccept)
	}
}

func TestLatestErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{"404", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) }, "HTTP 404"},
		{"500", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }, "HTTP 500"},
		{"malformed", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{nope")) }, "decode"},
		{"empty tag", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"html_url":"x"}`)) }, "no tag_name"},
		{"too large", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"tag_name":"v0.8.0","body":"` + strings.Repeat("x", maxBody) + `"}`))
		}, "decode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newServer(t, tt.handler)
			_, err := c.Latest(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestLatestHonoursContext(t *testing.T) {
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Latest(ctx); err == nil {
		t.Error("want an error once the context expires")
	}
}

func TestNewDefaults(t *testing.T) {
	c := New("dev")
	if c.URL != DefaultURL || c.HTTP == nil || c.HTTP.Timeout != 5*time.Second || c.UserAgent != "rootout/dev" {
		t.Errorf("New = %+v", c)
	}
}

func TestLatestTransportErrorNamesURLOnce(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	c := New("v0.7.0")
	c.URL = srv.URL
	_, err := c.Latest(context.Background())
	if err == nil {
		t.Fatal("want an error from a closed server")
	}
	if n := strings.Count(err.Error(), srv.URL); n != 1 {
		t.Errorf("URL appears %d times in %q, want once", n, err)
	}
}
