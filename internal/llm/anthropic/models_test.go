package anthropic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"
)

func modelJSON(id, lifecycle, created string) string {
	return fmt.Sprintf(`{"id":%q,"type":"model","display_name":"Name %s","created_at":%q,"lifecycle":%q}`, id, id, created, lifecycle)
}

func TestListModels(t *testing.T) {
	page1 := `{"data":[` + modelJSON("old", "active", "2025-01-01T00:00:00Z") + `,` + modelJSON("gone", "retired", "2024-01-01T00:00:00Z") + `],"has_more":true,"first_id":"old","last_id":"gone"}`
	page2 := `{"data":[` + modelJSON("new", "active", "2026-06-01T00:00:00Z") + `,` + modelJSON("dep", "deprecated", "2025-06-01T00:00:00Z") + `],"has_more":false,"first_id":"new","last_id":"dep"}`
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "sk-test" {
			t.Errorf("x-api-key = %q", r.Header.Get("x-api-key"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("after_id") {
		case "":
			_, _ = w.Write([]byte(page1))
		case "gone":
			_, _ = w.Write([]byte(page2))
		default:
			t.Errorf("unexpected after_id %q", r.URL.Query().Get("after_id"))
		}
	}))
	defer srv.Close()

	got, err := ListModels(context.Background(), " sk-test\n", option.WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("requests = %d, want 2", calls)
	}
	var ids []string
	for _, m := range got {
		ids = append(ids, m.ID)
	}
	if want := "new dep old"; strings.Join(ids, " ") != want {
		t.Fatalf("ids = %v, want %q (newest first, retired dropped)", ids, want)
	}
	if got[0].DisplayName != "Name new" || got[0].Created.Year() != 2026 {
		t.Errorf("first = %+v", got[0])
	}
	if got[0].Deprecated || !got[1].Deprecated || got[2].Deprecated {
		t.Errorf("deprecated flags wrong: %+v", got)
	}
}

func TestListModelsPageCap(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		id := fmt.Sprintf("m%d", calls)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":[%s],"has_more":true,"first_id":%q,"last_id":%q}`, modelJSON(id, "active", "2026-01-01T00:00:00Z"), id, id)
	}))
	defer srv.Close()
	got, err := ListModels(context.Background(), "sk-test", option.WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if calls != maxModelPages || len(got) != maxModelPages {
		t.Errorf("requests = %d, models = %d, want %d each", calls, len(got), maxModelPages)
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
		{"server error", 500, `{"type":"error","error":{"type":"api_error","message":"boom"}}`, false},
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
	if _, err := ListModels(context.Background(), " "); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Errorf("err = %v, want an API key error without a request", err)
	}
}
