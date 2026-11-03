package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/shiva2021/rootout/internal/llm"
)

// recordingVerify returns a Verify func that records its calls and answers
// with the given models or error.
func recordingVerify(models []llm.ModelInfo, err error) (func(context.Context, string, string) ([]llm.ModelInfo, error), *[][2]string) {
	var calls [][2]string
	return func(_ context.Context, provider, key string) ([]llm.ModelInfo, error) {
		calls = append(calls, [2]string{provider, key})
		return models, err
	}, &calls
}

// currentHosted is a CurrentHosted that answers the same model for any provider.
func currentHosted(model string) func(string) string { return func(string) string { return model } }

func newTestSetup(t *testing.T, d SetupDeps) *Setup {
	t.Helper()
	s := NewSetup(context.Background(), d)
	s.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return s
}

func pressSetup(s *Setup, keys ...tea.KeyPressMsg) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		_, cmd = s.Update(k)
	}
	return cmd
}

func viewSetup(s *Setup) string { return plain(s.View().Content) }

// pumpSetup runs cmd and feeds every non-tick message back into s.
func pumpSetup(s *Setup, cmd tea.Cmd) {
	for _, msg := range runCmd(cmd) {
		if _, tick := msg.(spinner.TickMsg); tick || msg == nil {
			continue
		}
		_, next := s.Update(msg)
		pumpSetup(s, next)
	}
}

func typeSetup(s *Setup, text string) {
	for _, r := range text {
		s.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// toHostedKey opens the Claude key screen.
func toHostedKey(t *testing.T, s *Setup) {
	t.Helper()
	pressSetup(s, keyDown, keyEnter)
	if !strings.Contains(viewSetup(s), "console.anthropic.com") {
		t.Fatalf("key screen not shown:\n%s", viewSetup(s))
	}
}

func TestSetupMenuView(t *testing.T) {
	s := newTestSetup(t, SetupDeps{})
	out := viewSetup(s)
	for _, want := range []string{"Welcome to rootout", "recommends keep or uninstall", "Local model (Ollama)", "Claude (Anthropic)", "OpenAI", "Not now", "rootout setup"} {
		if !strings.Contains(out, want) {
			t.Errorf("menu missing %q:\n%s", want, out)
		}
	}
	lines := strings.Split(out, "\n")
	footer := strings.TrimRight(lines[len(lines)-1], " ")
	if !strings.HasSuffix(footer, "? help") || !strings.Contains(footer, "esc skip") {
		t.Errorf("footer = %q", footer)
	}
	if len(lines) != 30 {
		t.Errorf("view has %d lines, want 30", len(lines))
	}
	for _, w := range []int{20, 60, 200} {
		s.Update(tea.WindowSizeMsg{Width: w, Height: 12})
		_ = viewSetup(s)
	}
}

func TestSetupEscSkips(t *testing.T) {
	s := newTestSetup(t, SetupDeps{})
	assertQuit(t, pressSetup(s, keyEsc))
	res, ok := s.Result()
	if !ok || !res.Skipped || res.Provider != "" || s.quitAll {
		t.Errorf("Result() = %+v, %v (quitAll %v); want Skipped", res, ok, s.quitAll)
	}
}

func TestSetupNotNowRow(t *testing.T) {
	s := newTestSetup(t, SetupDeps{})
	assertQuit(t, pressSetup(s, keyDown, keyDown, keyDown, keyEnter))
	if res, ok := s.Result(); !ok || !res.Skipped {
		t.Errorf("Result() = %+v, %v; want Skipped", res, ok)
	}
}

func TestSetupCtrlXQuitsAll(t *testing.T) {
	for _, k := range []tea.KeyPressMsg{keyCtrlX, keyCtrlC} {
		s := newTestSetup(t, SetupDeps{})
		assertQuit(t, pressSetup(s, k))
		if _, ok := s.Result(); ok || !s.quitAll {
			t.Errorf("%s: ok %v quitAll %v; want quitAll with no result", k, ok, s.quitAll)
		}
	}
}

func TestSetupHelpOverlay(t *testing.T) {
	s := newTestSetup(t, SetupDeps{})
	pressSetup(s, keyQuestion)
	if out := viewSetup(s); !strings.Contains(out, "Keys") || !strings.Contains(out, "any key to close") {
		t.Fatalf("help overlay not shown:\n%s", out)
	}
	pressSetup(s, keyDown)
	if out := viewSetup(s); strings.Contains(out, "any key to close") {
		t.Fatalf("help overlay should close on any key:\n%s", out)
	}
}

func TestSetupHostedKeyVerifiedAndSaved(t *testing.T) {
	verify, calls := recordingVerify(hostedFixture(), nil)
	s := newTestSetup(t, SetupDeps{Verify: verify, CurrentHosted: currentHosted("claude-sonnet-5-5")})
	toHostedKey(t, s)
	typeSetup(s, "sk-ant-secret-key")
	out := viewSetup(s)
	if strings.Contains(out, "sk-ant-secret-key") || !strings.Contains(out, "****") {
		t.Fatalf("key should be masked:\n%s", out)
	}
	cmd := pressSetup(s, keyEnter)
	if cmd == nil || !strings.Contains(viewSetup(s), "Checking the key") {
		t.Fatalf("enter should start verification:\n%s", viewSetup(s))
	}
	pumpSetup(s, cmd)
	if len(*calls) != 1 || (*calls)[0] != [2]string{"anthropic", "sk-ant-secret-key"} {
		t.Fatalf("Verify calls = %v", *calls)
	}
	out = viewSetup(s)
	if !strings.Contains(out, "Claude (Anthropic) models") || !strings.Contains(out, "› claude-sonnet-5-5 (current)") || strings.Contains(out, "Checking the key") {
		t.Fatalf("model picker with the current model selected expected after the check:\n%s", out)
	}
	if _, ok := s.Result(); ok {
		t.Fatal("no result before a model is chosen")
	}
	assertQuit(t, pressSetup(s, keyUp, keyEnter))
	res, ok := s.Result()
	if !ok || res.Provider != "anthropic" || res.Key != "sk-ant-secret-key" || !res.KeyVerified || res.Skipped || res.Model != "claude-new" {
		t.Errorf("Result() = %+v, %v", res, ok)
	}
}

func TestSetupHostedPickerEscReturnsToKeyScreen(t *testing.T) {
	verify, _ := recordingVerify(hostedFixture(), nil)
	s := newTestSetup(t, SetupDeps{Verify: verify, CurrentHosted: currentHosted("claude-sonnet-5-5")})
	toHostedKey(t, s)
	typeSetup(s, "sk-ant-secret-key")
	pumpSetup(s, pressSetup(s, keyEnter))
	if !strings.Contains(viewSetup(s), "Claude (Anthropic) models") {
		t.Fatalf("picker not shown:\n%s", viewSetup(s))
	}
	if cmd := pressSetup(s, keyEsc); cmd != nil && isQuit(cmd) {
		t.Fatal("esc in the picker should go back, not quit")
	}
	out := viewSetup(s)
	if s.picker != nil || !strings.Contains(out, "console.anthropic.com") || strings.Contains(out, "Checking the key") {
		t.Fatalf("key screen not restored after esc:\n%s", out)
	}
	if _, ok := s.Result(); ok {
		t.Fatal("esc must not produce a result")
	}
	// The typed key is still there: enter checks again and the picker returns.
	cmd := pressSetup(s, keyEnter)
	if cmd == nil {
		t.Fatal("enter should re-run the check")
	}
	pumpSetup(s, cmd)
	assertQuit(t, pressSetup(s, keyEnter))
	if res, ok := s.Result(); !ok || res.Model != "claude-sonnet-5-5" || res.Key != "sk-ant-secret-key" {
		t.Errorf("Result() = %+v, %v", res, ok)
	}
}

func TestSetupHostedPickerTypedModel(t *testing.T) {
	verify, _ := recordingVerify(nil, nil) // the key works but the list is empty
	s := newTestSetup(t, SetupDeps{Verify: verify, CurrentHosted: currentHosted("gpt-5.2")})
	pressSetup(s, keyDown, keyDown, keyEnter) // OpenAI
	typeSetup(s, "sk-proj-abc")
	pumpSetup(s, pressSetup(s, keyEnter))
	if out := viewSetup(s); !strings.Contains(out, "OpenAI models") || !strings.Contains(out, "› gpt-5.2 (current)") {
		t.Fatalf("empty list should still offer the current model:\n%s", out)
	}
	pressSetup(s, runeKey('t'))
	typeSetup(s, "-pro")
	assertQuit(t, pressSetup(s, keyEnter))
	if res, ok := s.Result(); !ok || res.Provider != "openai" || res.Model != "gpt-5.2-pro" || !res.KeyVerified {
		t.Errorf("Result() = %+v, %v", res, ok)
	}
}

func TestSetupHostedPickerCtrlXQuitsAll(t *testing.T) {
	verify, _ := recordingVerify(hostedFixture(), nil)
	s := newTestSetup(t, SetupDeps{Verify: verify})
	toHostedKey(t, s)
	typeSetup(s, "sk-ant-secret-key")
	pumpSetup(s, pressSetup(s, keyEnter))
	assertQuit(t, pressSetup(s, keyCtrlX))
	if _, ok := s.Result(); ok || !s.quitAll || s.picker != nil {
		t.Errorf("ctrl+x in the picker: ok %v quitAll %v picker %v", ok, s.quitAll, s.picker != nil)
	}
}

func TestSetupHostedKeyTrimsPaste(t *testing.T) {
	verify, calls := recordingVerify(hostedFixture(), nil)
	s := newTestSetup(t, SetupDeps{Verify: verify})
	pressSetup(s, keyDown, keyDown, keyEnter) // OpenAI
	if !strings.Contains(viewSetup(s), "platform.openai.com") {
		t.Fatalf("OpenAI key screen not shown:\n%s", viewSetup(s))
	}
	s.Update(tea.PasteMsg{Content: "sk-proj-abc\n"})
	pumpSetup(s, pressSetup(s, keyEnter))
	if len(*calls) != 1 || (*calls)[0] != [2]string{"openai", "sk-proj-abc"} {
		t.Fatalf("Verify calls = %v, want the trimmed key", *calls)
	}
}

func TestSetupHostedVerifyFailsRetryOrSave(t *testing.T) {
	verify, calls := recordingVerify(nil, errors.New("anthropic: the API key was rejected (401)"))
	s := newTestSetup(t, SetupDeps{Verify: verify})
	toHostedKey(t, s)
	typeSetup(s, "sk-bad")
	pumpSetup(s, pressSetup(s, keyEnter))
	out := viewSetup(s)
	if !strings.Contains(out, "rejected (401)") || !strings.Contains(out, "r retry") || !strings.Contains(out, "s save anyway") {
		t.Fatalf("failure state not shown:\n%s", out)
	}
	if _, ok := s.Result(); ok {
		t.Fatal("no result yet")
	}
	// r verifies again with the same text.
	cmd := pressSetup(s, runeKey('r'))
	if cmd == nil {
		t.Fatal("r should re-run the check")
	}
	pumpSetup(s, cmd)
	if len(*calls) != 2 || (*calls)[1][1] != "sk-bad" {
		t.Fatalf("Verify calls = %v", *calls)
	}
	// s saves anyway with a warning.
	assertQuit(t, pressSetup(s, runeKey('s')))
	res, ok := s.Result()
	if !ok || res.Provider != "anthropic" || res.Key != "sk-bad" || res.KeyVerified || res.Model != "" || !strings.Contains(res.Warning, "401") {
		t.Errorf("Result() = %+v, %v (save anyway skips the model picker)", res, ok)
	}
}

func TestSetupHostedEscGoesBack(t *testing.T) {
	s := newTestSetup(t, SetupDeps{})
	toHostedKey(t, s)
	typeSetup(s, "abc")
	if cmd := pressSetup(s, keyEsc); cmd != nil && isQuit(cmd) {
		t.Fatal("esc on the key screen should go back, not quit")
	}
	if out := viewSetup(s); !strings.Contains(out, "Welcome to rootout") {
		t.Fatalf("menu not shown after esc:\n%s", out)
	}
}

func TestSetupHostedEnterWithoutKey(t *testing.T) {
	verify, calls := recordingVerify(hostedFixture(), nil)
	s := newTestSetup(t, SetupDeps{Verify: verify})
	toHostedKey(t, s)
	if cmd := pressSetup(s, keyEnter); cmd != nil {
		t.Fatal("enter with nothing typed and no stored key should do nothing")
	}
	if out := viewSetup(s); !strings.Contains(out, "paste a key first") {
		t.Fatalf("hint missing:\n%s", out)
	}
	if len(*calls) != 0 {
		t.Fatalf("Verify called: %v", *calls)
	}
}

func TestSetupHostedExistingKeyKept(t *testing.T) {
	verify, calls := recordingVerify(hostedFixture(), nil)
	existing := func(provider string) (string, string, error) {
		if provider == "anthropic" {
			return "sk-ant-…WXYZ", "keychain", nil
		}
		return "", "", nil
	}
	s := newTestSetup(t, SetupDeps{Verify: verify, Existing: existing, CurrentHosted: currentHosted("claude-sonnet-5-5")})
	toHostedKey(t, s)
	if out := viewSetup(s); !strings.Contains(out, "Stored key sk-ant-…WXYZ") || !strings.Contains(out, "enter keeps it") {
		t.Fatalf("stored-key note missing:\n%s", out)
	}
	pumpSetup(s, pressSetup(s, keyEnter))
	if len(*calls) != 1 || (*calls)[0] != [2]string{"anthropic", ""} {
		t.Fatalf("Verify calls = %v, want an empty key (keep the stored one)", *calls)
	}
	assertQuit(t, pressSetup(s, keyEnter))
	res, ok := s.Result()
	if !ok || res.Provider != "anthropic" || res.Key != "" || !res.KeyVerified || res.Model != "claude-sonnet-5-5" {
		t.Errorf("Result() = %+v, %v", res, ok)
	}
}

func TestSetupHostedExistingKeyNotes(t *testing.T) {
	tests := []struct {
		name   string
		source string
		err    error
		want   string
	}{
		{"env", "env", nil, "ANTHROPIC_API_KEY"},
		{"keychain error", "", errors.New("keychain: locked"), "Keychain unavailable: keychain: locked"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			existing := func(string) (string, string, error) {
				if tt.err != nil {
					return "", "", tt.err
				}
				return "sk-ant-…WXYZ", tt.source, nil
			}
			s := newTestSetup(t, SetupDeps{Existing: existing})
			toHostedKey(t, s)
			if out := viewSetup(s); !strings.Contains(out, tt.want) {
				t.Fatalf("note %q missing:\n%s", tt.want, out)
			}
		})
	}
}

func TestSetupOllamaUnavailable(t *testing.T) {
	s := newTestSetup(t, SetupDeps{ModelsErr: "no hardware info"})
	if out := viewSetup(s); !strings.Contains(out, "unavailable: no hardware info") {
		t.Fatalf("row should say why:\n%s", out)
	}
	if cmd := pressSetup(s, keyEnter); cmd != nil {
		t.Fatal("enter on the unavailable row should do nothing")
	}
	if !strings.Contains(viewSetup(s), "Welcome to rootout") {
		t.Fatal("should stay on the menu")
	}
}

func TestSetupOllamaPickerChoosesModel(t *testing.T) {
	deps := SetupDeps{Models: &ModelDeps{Client: ollamaServer(t), Hardware: testHW}, Current: "llama3.2:3b"}
	s := newTestSetup(t, deps)
	cmd := pressSetup(s, keyEnter)
	if cmd == nil || !strings.Contains(viewSetup(s), "looking for Ollama") {
		t.Fatalf("enter should open the picker and start detection:\n%s", viewSetup(s))
	}
	pumpSetup(s, cmd)
	out := viewSetup(s)
	if !strings.Contains(out, "llama3.2:3b (current)") || strings.Contains(out, "Welcome to rootout") {
		t.Fatalf("picker list not shown:\n%s", out)
	}
	s.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	if mp, ok := s.picker.(*ModelPicker); !ok || mp.width != 140 {
		t.Fatal("resize should reach the embedded picker")
	}
	assertQuit(t, pressSetup(s, keyEnter))
	res, ok := s.Result()
	if !ok || res.Provider != "ollama" || res.Model != "llama3.2:3b" {
		t.Errorf("Result() = %+v, %v", res, ok)
	}
}

func TestSetupOllamaPickerEscGoesBack(t *testing.T) {
	deps := SetupDeps{Models: &ModelDeps{Client: ollamaServer(t), Hardware: testHW}}
	s := newTestSetup(t, deps)
	pumpSetup(s, pressSetup(s, keyEnter))
	if cmd := pressSetup(s, keyEsc); cmd != nil && isQuit(cmd) {
		t.Fatal("esc in the picker should go back to the menu")
	}
	if s.picker != nil || !strings.Contains(viewSetup(s), "Welcome to rootout") {
		t.Fatalf("menu not shown after esc:\n%s", viewSetup(s))
	}
}

func TestSetupOllamaPickerCtrlXQuitsAll(t *testing.T) {
	deps := SetupDeps{Models: &ModelDeps{Client: ollamaServer(t), Hardware: testHW}}
	s := newTestSetup(t, deps)
	pumpSetup(s, pressSetup(s, keyEnter))
	assertQuit(t, pressSetup(s, keyCtrlX))
	if _, ok := s.Result(); ok || !s.quitAll || s.picker != nil {
		t.Errorf("ctrl+x in the picker: ok %v quitAll %v picker %v", ok, s.quitAll, s.picker != nil)
	}
}
