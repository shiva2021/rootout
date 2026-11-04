package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/ext"
	"github.com/shiva2021/rootout/internal/heur"
	"github.com/shiva2021/rootout/internal/llm"
)

// messageBody is a minimal Messages API reply carrying text as its one text block.
func messageBody(text string) string {
	b, _ := json.Marshal(map[string]any{
		"id": "msg_01", "type": "message", "role": "assistant", "model": "claude-test",
		"content":     []map[string]any{{"type": "text", "text": text}},
		"stop_reason": "end_turn", "stop_sequence": nil,
		"usage": map[string]any{"input_tokens": 100, "output_tokens": 20},
	})
	return string(b)
}

const authError = `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`

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
			body: messageBody(`{"verdict":"malicious","confidence":0.9,"reasoning":"Reads keys.","iocs":["x.example"]}`),
			want: llm.Assessment{Verdict: llm.Malicious, Confidence: 0.9, Reasoning: "Reads keys.", IOCs: []string{"x.example"}, Provider: "anthropic", Model: "claude-test"}},
		{name: "normalised verdict", status: 200,
			body: messageBody(`{"verdict":"MALICIOUS","confidence":1.5,"reasoning":"r","iocs":[]}`),
			want: llm.Assessment{Verdict: llm.Malicious, Confidence: 1, Reasoning: "r", IOCs: []string{}, Provider: "anthropic", Model: "claude-test"}},
		{name: "unknown verdict", status: 200,
			body: messageBody(`{"verdict":"evil","confidence":0.5,"reasoning":"r","iocs":[]}`),
			want: llm.Assessment{Verdict: llm.Unknown, Confidence: 0.5, Reasoning: "r", IOCs: []string{}, Provider: "anthropic", Model: "claude-test"}},
		{name: "malformed", status: 200, body: messageBody(`{"verdict":`),
			wantErr: func(err error) bool { return errors.Is(err, llm.ErrBadResponse) }},
		{name: "unauthorised", status: 401, body: authError,
			wantErr: func(err error) bool { return strings.Contains(err.Error(), "authentication") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
					t.Errorf("request %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("x-api-key") != "sk-test" || r.Header.Get("anthropic-version") == "" {
					t.Errorf("headers: x-api-key=%q anthropic-version=%q", r.Header.Get("x-api-key"), r.Header.Get("anthropic-version"))
				}
				var req struct {
					Model        string `json:"model"`
					MaxTokens    int    `json:"max_tokens"`
					System       []struct{ Text string }
					OutputConfig struct {
						Format struct {
							Type   string         `json:"type"`
							Schema map[string]any `json:"schema"`
						} `json:"format"`
					} `json:"output_config"`
					ToolChoice any `json:"tool_choice"`
				}
				data, _ := io.ReadAll(r.Body)
				if err := json.Unmarshal(data, &req); err != nil {
					t.Fatalf("body: %v", err)
				}
				f := req.OutputConfig.Format
				if req.Model != "claude-test" || req.MaxTokens != 1024 || f.Type != "json_schema" || f.Schema["properties"] == nil {
					t.Errorf("request body: %s", data)
				}
				if req.ToolChoice != nil || len(req.System) != 1 || !strings.Contains(string(data), "eval_dynamic") {
					t.Errorf("request body: %s", data)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer srv.Close()
			p := New("sk-test", "claude-test", option.WithBaseURL(srv.URL), option.WithMaxRetries(0))
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
			OutputConfig struct {
				Format struct {
					Schema map[string]any `json:"schema"`
				} `json:"format"`
			} `json:"output_config"`
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &req)
		schema = req.OutputConfig.Format.Schema
		if !strings.Contains(string(data), "com.google.Chrome") {
			t.Errorf("request body lacks the app facts: %s", data)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, messageBody(`{"purpose":"Browses the web.","source":"legitimate","confidence":0.95,"recommendation":"keep","reasoning":"Signed by Google."}`))
	}))
	defer srv.Close()
	p := New("sk-test", "claude-test", option.WithBaseURL(srv.URL), option.WithMaxRetries(0))
	a, err := llm.AssessApp(context.Background(), p, app.Facts{App: app.App{Name: "Chrome", BundleID: "com.google.Chrome"}})
	if err != nil {
		t.Fatal(err)
	}
	if props, _ := schema["properties"].(map[string]any); props["purpose"] == nil || props["verdict"] != nil {
		t.Errorf("request schema = %v, want the app schema", schema)
	}
	if a.Recommendation != llm.Keep || a.Source != llm.SourceLegitimate || a.Provider != "anthropic" || a.Model != "claude-test" {
		t.Errorf("a = %+v", a)
	}
}
