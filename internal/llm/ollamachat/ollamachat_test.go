package ollamachat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shiva2021/rootout/internal/ext"
	"github.com/shiva2021/rootout/internal/heur"
	"github.com/shiva2021/rootout/internal/llm"
)

// chatBody is a minimal non-streaming /api/chat reply (shape from docs/api.md).
func chatBody(content string) string {
	b, _ := json.Marshal(map[string]any{
		"model": "qwen3:8b", "created_at": "2026-10-20T10:00:00Z",
		"message":     map[string]any{"role": "assistant", "content": content},
		"done_reason": "stop", "done": true,
		"total_duration": 2254970291, "load_duration": 1000, "prompt_eval_count": 34,
		"prompt_eval_duration": 1000, "eval_count": 12, "eval_duration": 1000,
	})
	return string(b)
}

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
			body: chatBody(`{"verdict":"benign","confidence":0.8,"reasoning":"Formatter.","iocs":[]}`),
			want: llm.Assessment{Verdict: llm.Benign, Confidence: 0.8, Reasoning: "Formatter.", IOCs: []string{}, Provider: "ollama", Model: "qwen3:8b"}},
		{name: "normalised verdict", status: 200,
			body: chatBody(`{"verdict":"MALICIOUS","confidence":0.7,"reasoning":"r","iocs":["1.2.3.4"]}`),
			want: llm.Assessment{Verdict: llm.Malicious, Confidence: 0.7, Reasoning: "r", IOCs: []string{"1.2.3.4"}, Provider: "ollama", Model: "qwen3:8b"}},
		{name: "unknown verdict", status: 200,
			body: chatBody(`{"verdict":"evil","confidence":0.5,"reasoning":"r","iocs":[]}`),
			want: llm.Assessment{Verdict: llm.Unknown, Confidence: 0.5, Reasoning: "r", IOCs: []string{}, Provider: "ollama", Model: "qwen3:8b"}},
		{name: "malformed content", status: 200, body: chatBody(`{"verdict":`),
			wantErr: func(err error) bool { return errors.Is(err, llm.ErrBadResponse) }},
		{name: "malformed envelope", status: 200, body: `<html>`,
			wantErr: func(err error) bool { return errors.Is(err, llm.ErrBadResponse) }},
		{name: "unauthorised", status: 401, body: `{"error":"unauthorized"}`,
			wantErr: func(err error) bool { return strings.Contains(err.Error(), "authentication") }},
		{name: "model missing", status: 404, body: `{"error":"model 'qwen3:8b' not found"}`,
			wantErr: func(err error) bool { return strings.Contains(err.Error(), "not found") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/chat" {
					t.Errorf("request %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "" || r.Header.Get("x-api-key") != "" {
					t.Error("unexpected auth header")
				}
				var req struct {
					Model    string `json:"model"`
					Messages []struct {
						Role    string `json:"role"`
						Content string `json:"content"`
					} `json:"messages"`
					Stream  *bool          `json:"stream"`
					Think   *bool          `json:"think"`
					Format  map[string]any `json:"format"`
					Options map[string]any `json:"options"`
				}
				data, _ := io.ReadAll(r.Body)
				if err := json.Unmarshal(data, &req); err != nil {
					t.Fatalf("body: %v", err)
				}
				if req.Model != "qwen3:8b" || req.Stream == nil || *req.Stream || req.Think == nil || *req.Think ||
					req.Format["properties"] == nil || req.Options["temperature"] != float64(0) ||
					len(req.Messages) != 2 || req.Messages[0].Role != "system" || req.Messages[1].Role != "user" ||
					!strings.Contains(req.Messages[1].Content, "eval_dynamic") {
					t.Errorf("request body: %s", data)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer srv.Close()
			p := New(srv.URL+"/", "qwen3:8b", srv.Client())
			got, err := p.Assess(context.Background(), report)
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

func TestNoModel(t *testing.T) {
	_, err := New("", "", nil).Assess(context.Background(), report)
	if !errors.Is(err, ErrNoModel) || !strings.Contains(err.Error(), "rootout config models") {
		t.Errorf("err = %v", err)
	}
}

// Ollama 0.40 answers 501 "structured output is unavailable" for models its
// new engine cannot constrain; the adapter must retry without a format.
func TestAssessFallsBackWithoutFormat(t *testing.T) {
	var formats []bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		_, has := req["format"]
		formats = append(formats, has)
		if has {
			w.WriteHeader(http.StatusNotImplemented)
			_, _ = w.Write([]byte(`{"error":"structured output is unavailable"}`))
			return
		}
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"Here is my assessment:\n` + "```json\n" + `{\"verdict\":\"Malicious\",\"confidence\":100,\"reasoning\":\"exfiltrates keys\",\"iocs\":[\"discord.com/api/webhooks\"]}` + "\n```" + `"},"done":true}`))
	}))
	defer srv.Close()
	p := New(srv.URL, "qwen3.5:4b", srv.Client())
	a, err := p.Assess(context.Background(), report)
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if len(formats) != 2 || !formats[0] || formats[1] {
		t.Fatalf("requests with format = %v, want [true false]", formats)
	}
	if a.Verdict != llm.Malicious || a.Confidence != 1 || len(a.IOCs) != 1 {
		t.Fatalf("assessment = %+v", a)
	}
}
