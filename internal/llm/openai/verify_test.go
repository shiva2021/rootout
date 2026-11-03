package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3/option"
)

func TestVerify(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		wantErr  bool
		wantAuth bool
	}{
		{name: "ok", status: 200, body: `{"object":"list","data":[{"id":"gpt-test","object":"model","created":1760000000,"owned_by":"openai"}]}`},
		{name: "unauthorised", status: 401, body: authError, wantErr: true, wantAuth: true},
		{name: "forbidden", status: 403, body: authError, wantErr: true, wantAuth: true},
		{name: "server error", status: 500, body: `{"error":{"message":"boom","type":"server_error"}}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			err := Verify(context.Background(), " sk-test\n", option.WithBaseURL(srv.URL))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantAuth != (err != nil && strings.Contains(err.Error(), "API key")) {
				t.Errorf("err = %v, wantAuth %v", err, tt.wantAuth)
			}
			if calls != 1 {
				t.Errorf("requests = %d, want 1 (no retries)", calls)
			}
		})
	}
}

func TestVerifyEmptyKey(t *testing.T) {
	if err := Verify(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Errorf("err = %v, want an API key error without a request", err)
	}
}
