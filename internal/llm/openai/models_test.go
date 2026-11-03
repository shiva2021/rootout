package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3/option"
)

func TestChatCapable(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"gpt-5.2", true},
		{"gpt-4.1-mini", true},
		{"o3", true},
		{"o4-mini", true},
		{"gpt-4o-mini-tts", false},
		{"text-embedding-3-small", false},
		{"whisper-1", false},
		{"dall-e-3", false},
		{"gpt-image-1", false},
		{"gpt-4o-realtime-preview", false},
		{"gpt-4o-transcribe", false},
		{"gpt-4o-audio-preview", false},
		{"omni-moderation-latest", false},
		{"chatgpt-4o-latest", false},
		{"gpt-4o-search-preview", false},
		{"gpt-4o-mini-search-preview", false},
		{"gpt-3.5-turbo-instruct", false},
		{"o1-pro", true},
		{"babbage-002", false},
		{"omni-thing", false},
	}
	for _, tt := range tests {
		if got := chatCapable(tt.id); got != tt.want {
			t.Errorf("chatCapable(%q) = %v, want %v", tt.id, got, tt.want)
		}
	}
}

func TestListModels(t *testing.T) {
	body := `{"object":"list","data":[
		{"id":"gpt-4.1","object":"model","created":1700000000,"owned_by":"openai"},
		{"id":"whisper-1","object":"model","created":1800000000,"owned_by":"openai"},
		{"id":"gpt-5.2","object":"model","created":1760000000,"owned_by":"openai"},
		{"id":"o3","object":"model","created":1750000000,"owned_by":"openai","shutdown_date":"2027-01-01"}
	]}`
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/models" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	got, err := ListModels(context.Background(), " sk-test\n", option.WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("requests = %d, want 1", calls)
	}
	var ids []string
	for _, m := range got {
		ids = append(ids, m.ID)
	}
	if want := "gpt-5.2 o3 gpt-4.1"; strings.Join(ids, " ") != want {
		t.Fatalf("ids = %v, want %q (chat models only, newest first)", ids, want)
	}
	if got[0].Deprecated || !got[1].Deprecated || got[0].Created.Year() != 2025 {
		t.Errorf("flags/dates wrong: %+v", got)
	}
}

func TestListModelsErrors(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		wantAuth bool
	}{
		{"unauthorised", 401, authError, true},
		{"forbidden", 403, authError, true},
		{"server error", 500, `{"error":{"message":"boom","type":"server_error"}}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			_, err := ListModels(context.Background(), "sk-test", option.WithBaseURL(srv.URL))
			if err == nil {
				t.Fatal("want error")
			}
			if tt.wantAuth != strings.Contains(err.Error(), "API key") {
				t.Errorf("err = %v, wantAuth %v", err, tt.wantAuth)
			}
			if calls != 1 {
				t.Errorf("requests = %d, want 1 (no retries)", calls)
			}
		})
	}
}

func TestListModelsEmptyKey(t *testing.T) {
	if _, err := ListModels(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Errorf("err = %v, want an API key error without a request", err)
	}
}
