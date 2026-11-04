package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3/option"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/ext"
	"github.com/shiva2021/rootout/internal/heur"
	"github.com/shiva2021/rootout/internal/llm"
)

// responseBody is a minimal Responses API reply with one output_text part.
func responseBody(text string) string {
	b, _ := json.Marshal(map[string]any{
		"id": "resp_01", "object": "response", "created_at": 1760000000, "status": "completed", "model": "gpt-test",
		"output": []map[string]any{{
			"type": "message", "id": "msg_01", "status": "completed", "role": "assistant",
			"content": []map[string]any{{"type": "output_text", "text": text, "annotations": []any{}}},
		}},
		"usage": map[string]any{"input_tokens": 100, "output_tokens": 20, "total_tokens": 120},
	})
	return string(b)
}

const authError = `{"error":{"message":"Incorrect API key provided","type":"invalid_request_error","param":null,"code":"invalid_api_key"}}`

var report = heur.Report{Ext: ext.Extension{ID: "a.b"}, Score: 40,
	Findings: []heur.Finding{{Rule: "eval_dynamic", Severity: heur.High, File: "x.js", Line: 1, Snippet: "eval(x)", Detail: "eval"}}}

func TestAssess(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		want    llm.Assessment
		wantErr func(error) bool
	}{
		{name: "ok", status: 200,
			body: responseBody(`{"verdict":"suspicious","confidence":0.6,"reasoning":"Posts data.","iocs":["x.example"]}`),
			want: llm.Assessment{Verdict: llm.Suspicious, Confidence: 0.6, Reasoning: "Posts data.", IOCs: []string{"x.example"}, Provider: "openai", Model: "gpt-test"}},
		{name: "normalised verdict", status: 200,
			body: responseBody(`{"verdict":"MALICIOUS","confidence":-1,"reasoning":"r","iocs":null}`),
			want: llm.Assessment{Verdict: llm.Malicious, Confidence: 0, Reasoning: "r", IOCs: []string{}, Provider: "openai", Model: "gpt-test"}},
		{name: "unknown verdict", status: 200,
			body: responseBody(`{"verdict":"evil","confidence":0.5,"reasoning":"r","iocs":[]}`),
			want: llm.Assessment{Verdict: llm.Unknown, Confidence: 0.5, Reasoning: "r", IOCs: []string{}, Provider: "openai", Model: "gpt-test"}},
		{name: "malformed", status: 200, body: responseBody(`not json`),
			wantErr: func(err error) bool { return errors.Is(err, llm.ErrBadResponse) }},
		{name: "unauthorised", status: 401, body: authError,
			wantErr: func(err error) bool { return strings.Contains(err.Error(), "authentication") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/responses" {
					t.Errorf("request %s %s", r.Method, r.URL.Path)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
					t.Errorf("Authorization = %q", got)
				}
				var req struct {
					Model        string `json:"model"`
					Instructions string `json:"instructions"`
					Input        string `json:"input"`
					Text         struct {
						Format struct {
							Type   string         `json:"type"`
							Name   string         `json:"name"`
							Strict bool           `json:"strict"`
							Schema map[string]any `json:"schema"`
						} `json:"format"`
					} `json:"text"`
				}
				data, _ := io.ReadAll(r.Body)
				if err := json.Unmarshal(data, &req); err != nil {
					t.Fatalf("body: %v", err)
				}
				f := req.Text.Format
				if req.Model != "gpt-test" || f.Type != "json_schema" || !f.Strict || f.Schema["properties"] == nil ||
					req.Instructions == "" || !strings.Contains(req.Input, "eval_dynamic") {
					t.Errorf("request body: %s", data)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer srv.Close()
			p := New("sk-test", "gpt-test", option.WithBaseURL(srv.URL), option.WithMaxRetries(0))
			got, err := llm.Assess(context.Background(), p, report)
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			gj, _ := json.Marshal(got)
			wj, _ := json.Marshal(tt.want)
			if string(gj) != string(wj) {
				t.Errorf("got %s\nwant %s", gj, wj)
			}
		})
	}
}

func TestNoKey(t *testing.T) {
	if _, err := llm.Assess(context.Background(), New("", "m"), report); !errors.Is(err, llm.ErrNoKey) {
		t.Errorf("err = %v", err)
	}
}

func TestAssessApp(t *testing.T) {
	var schema map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input string `json:"input"`
			Text  struct {
				Format struct {
					Schema map[string]any `json:"schema"`
				} `json:"format"`
			} `json:"text"`
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &req)
		schema = req.Text.Format.Schema
		if !strings.Contains(req.Input, "com.google.Chrome") {
			t.Errorf("request input lacks the app facts: %s", req.Input)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, responseBody(`{"purpose":"Browses the web.","source":"legitimate","confidence":0.95,"recommendation":"keep","reasoning":"Signed by Google."}`))
	}))
	defer srv.Close()
	p := New("sk-test", "gpt-test", option.WithBaseURL(srv.URL), option.WithMaxRetries(0))
	a, err := llm.AssessApp(context.Background(), p, app.Facts{App: app.App{Name: "Chrome", BundleID: "com.google.Chrome"}})
	if err != nil {
		t.Fatal(err)
	}
	if props, _ := schema["properties"].(map[string]any); props["purpose"] == nil || props["verdict"] != nil {
		t.Errorf("request schema = %v, want the app schema", schema)
	}
	if a.Recommendation != llm.Keep || a.Source != llm.SourceLegitimate || a.Provider != "openai" || a.Model != "gpt-test" {
		t.Errorf("a = %+v", a)
	}
}
