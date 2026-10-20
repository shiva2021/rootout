package heur

import (
	"strings"
	"testing"
	"time"

	"github.com/shiva2021/purge/internal/ext"
)

func hygExtRule(t *testing.T, name string) ExtRule {
	t.Helper()
	regMu.Lock()
	defer regMu.Unlock()
	for _, r := range extRules {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("ext rule %q not registered", name)
	return ExtRule{}
}

func hygFileRule(t *testing.T, name string) FileRule {
	t.Helper()
	regMu.Lock()
	defer regMu.Unlock()
	for _, r := range fileRules {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("file rule %q not registered", name)
	return FileRule{}
}

func hygFixNow(t *testing.T, at time.Time) {
	t.Helper()
	old := hygNow
	hygNow = func() time.Time { return at }
	t.Cleanup(func() { hygNow = old })
}

func TestHygieneOrphanedFolder(t *testing.T) {
	rule := hygExtRule(t, "orphaned_folder")
	tests := []struct {
		name string
		ext  ext.Extension
		want int
		sev  Severity
	}{
		{"active", ext.Extension{Orphaned: false}, 0, Info},
		{"orphan", ext.Extension{Orphaned: true}, 1, Medium},
		{"orphan with node_modules", ext.Extension{Orphaned: true, HasNodeModules: true}, 1, High},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := rule.Check(&ExtCtx{Ext: &tc.ext})
			if len(got) != tc.want {
				t.Fatalf("got %d findings, want %d: %+v", len(got), tc.want, got)
			}
			if tc.want > 0 {
				if got[0].Severity != tc.sev {
					t.Errorf("severity = %s, want %s", got[0].Severity, tc.sev)
				}
				if !strings.Contains(got[0].Detail, "after uninstall") {
					t.Errorf("detail = %q", got[0].Detail)
				}
			}
		})
	}
}

func TestHygieneStaleExtension(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	hygFixNow(t, now)
	rule := hygExtRule(t, "stale_extension")
	tests := []struct {
		name string
		ext  ext.Extension
		want int
		sev  Severity
	}{
		{"unknown date", ext.Extension{}, 0, Info},
		{"one month old", ext.Extension{UpdatedAt: now.AddDate(0, -1, 0)}, 0, Info},
		{"three years old", ext.Extension{UpdatedAt: now.AddDate(-3, 0, 0)}, 1, Low},
		{"three years old with node_modules", ext.Extension{UpdatedAt: now.AddDate(-3, 0, 0), HasNodeModules: true}, 1, Medium},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := rule.Check(&ExtCtx{Ext: &tc.ext})
			if len(got) != tc.want {
				t.Fatalf("got %d findings, want %d: %+v", len(got), tc.want, got)
			}
			if tc.want > 0 {
				if got[0].Severity != tc.sev {
					t.Errorf("severity = %s, want %s", got[0].Severity, tc.sev)
				}
				if !strings.Contains(got[0].Detail, "1096 days ago") {
					t.Errorf("detail = %q, want the age in days", got[0].Detail)
				}
			}
		})
	}
}

func TestHygieneVulnerableBundledDep(t *testing.T) {
	rule := hygExtRule(t, "vulnerable_bundled_dep")
	tests := []struct {
		name string
		deps map[string]string
		want int
		sev  Severity
	}{
		{
			name: "leetcode audit case",
			deps: map[string]string{
				"underscore": "1.9.1", "moment": "2.29.3", "qs": "6.5.2",
				"request": "2.88.0", "axios": "1.6.8", "form-data": "2.3.3",
			},
			want: 6, sev: High,
		},
		{
			name: "clean modern set",
			deps: map[string]string{
				"underscore": "1.13.7", "moment": "2.30.1", "qs": "6.13.0",
				"axios": "1.7.9", "lodash": "4.17.21", "ws": "8.18.0", "left-pad": "1.3.0",
			},
			want: 0,
		},
		{name: "none", deps: nil, want: 0},
		{name: "two hits stay medium", deps: map[string]string{"qs": "6.5.2", "ws": "7.4.5"}, want: 2, sev: Medium},
		{name: "caret at fixed version", deps: map[string]string{"lodash": "^4.17.21"}, want: 0},
		{name: "tilde below fix", deps: map[string]string{"minimist": "~1.2.5"}, want: 1, sev: Medium},
		{name: "gte floor below fix", deps: map[string]string{"node-fetch": ">=2.0.0"}, want: 1, sev: Medium},
		{name: "tag skipped", deps: map[string]string{"lodash": "latest"}, want: 0},
		{name: "git url skipped", deps: map[string]string{"lodash": "github:foo/bar"}, want: 0},
		{name: "deprecated any version", deps: map[string]string{"request": "latest"}, want: 1, sev: Medium},
		{name: "axios old branch", deps: map[string]string{"axios": "0.19.2"}, want: 2, sev: Medium},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := rule.Check(&ExtCtx{Ext: &ext.Extension{Dependencies: tc.deps}})
			if len(got) != tc.want {
				t.Fatalf("got %d findings, want %d: %+v", len(got), tc.want, got)
			}
			for _, f := range got {
				if f.Severity != tc.sev {
					t.Errorf("%s: severity = %s, want %s", f.Detail, f.Severity, tc.sev)
				}
			}
		})
	}

	t.Run("detail format", func(t *testing.T) {
		got := rule.Check(&ExtCtx{Ext: &ext.Extension{Dependencies: map[string]string{"underscore": "1.9.1"}}})
		if len(got) != 1 || got[0].Detail != "underscore@1.9.1 < 1.12.1 (CVE-2021-23358)" {
			t.Errorf("got %+v", got)
		}
	})
}

func TestHygieneSemver(t *testing.T) {
	tests := []struct {
		in   string
		want hygSemver
		ok   bool
	}{
		{"1.2.3", hygSemver{1, 2, 3}, true},
		{"^4.17.21", hygSemver{4, 17, 21}, true},
		{"~1.2.5", hygSemver{1, 2, 5}, true},
		{">=2.0.0", hygSemver{2, 0, 0}, true},
		{"v3.1", hygSemver{3, 1, 0}, true},
		{"2.29.3-beta.1", hygSemver{2, 29, 3}, true},
		{"1.0.0+build.7", hygSemver{1, 0, 0}, true},
		{"latest", hygSemver{}, false},
		{"github:foo/bar", hygSemver{}, false},
		{"*", hygSemver{}, false},
		{"", hygSemver{}, false},
		{"1.2.3.4", hygSemver{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, ok := hygParseSemver(tc.in)
			if ok != tc.ok || got != tc.want {
				t.Errorf("hygParseSemver(%q) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestHygieneUnknownSource(t *testing.T) {
	rule := hygExtRule(t, "unknown_source")
	tests := []struct {
		source string
		want   int
	}{
		{"gallery", 0},
		{"", 0},
		{"vsix", 1},
		{"local", 1},
	}
	for _, tc := range tests {
		t.Run("source="+tc.source, func(t *testing.T) {
			got := rule.Check(&ExtCtx{Ext: &ext.Extension{Source: tc.source}})
			if len(got) != tc.want {
				t.Fatalf("got %d findings, want %d: %+v", len(got), tc.want, got)
			}
			if tc.want > 0 && got[0].Severity != Info {
				t.Errorf("severity = %s, want info", got[0].Severity)
			}
		})
	}
}

// hygFileCase is one snippet run through a file rule.
type hygFileCase struct {
	name   string
	rel    string
	src    string
	events []string
	want   int
	sev    Severity
	detail string // substring expected in the first finding
}

func hygRunFileCases(t *testing.T, rule FileRule, cases []hygFileCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rel := tc.rel
			if rel == "" {
				rel = "dist/extension.js"
			}
			c := &FileCtx{
				Ext:  &ext.Extension{ID: "test.ext", ActivationEvents: tc.events},
				Rel:  rel,
				Data: []byte(tc.src),
			}
			got := rule.Check(c)
			if len(got) != tc.want {
				t.Fatalf("got %d findings, want %d: %+v", len(got), tc.want, got)
			}
			if tc.want == 0 {
				return
			}
			f := got[0]
			if f.Severity != tc.sev {
				t.Errorf("severity = %s, want %s", f.Severity, tc.sev)
			}
			if f.Rule != rule.Name {
				t.Errorf("rule = %q, want %q", f.Rule, rule.Name)
			}
			if f.File != rel || f.Line == 0 || f.Snippet == "" {
				t.Errorf("location not filled: %+v", f)
			}
			if tc.detail != "" && !strings.Contains(f.Detail, tc.detail) {
				t.Errorf("detail = %q, want it to contain %q", f.Detail, tc.detail)
			}
		})
	}
}

func TestHygienePermissionBypassLaunch(t *testing.T) {
	rule := hygFileRule(t, "permission_bypass_launch")
	hygRunFileCases(t, rule, []hygFileCase{
		{
			name: "kiro-style terminal launch",
			src:  "const term = vscode.window.createTerminal('Claude');\nterm.sendText('claude --permission-mode bypassPermissions');\n",
			want: 1, sev: High, detail: "safety prompts disabled",
		},
		{
			name:   "critical on startup",
			src:    "term.sendText('claude --permission-mode bypassPermissions');\n",
			events: []string{"onStartupFinished"},
			want:   1, sev: Critical,
		},
		{
			name:   "critical on star activation",
			src:    "spawn('claude', ['--dangerously-skip-permissions']);\n",
			events: []string{"*"},
			want:   1, sev: Critical, detail: "--dangerously-skip-permissions",
		},
		{
			name: "mcp list is fine",
			src:  "execFile('claude', ['mcp', 'list'], cb);\nexecFile('claude', ['mcp', 'get', name], cb);\n",
			want: 0,
		},
		{
			name: "no-sandbox near spawn",
			src:  "const p = spawn(bin, ['--no-sandbox', '--headless']);\n",
			want: 1, sev: High, detail: "--no-sandbox",
		},
		{
			name: "no-sandbox far from any launcher",
			src:  "const args = ['--no-sandbox'];\n" + strings.Repeat("// padding\n", 60) + "spawn(bin, args);\n",
			want: 0,
		},
		{
			name: "npx -y in sendText",
			src:  "terminal.sendText('npx -y some-agent@latest');\n",
			want: 1, sev: High, detail: "npx -y",
		},
		{
			name: "npx -y outside a launcher",
			src:  "const hint = 'run: npx -y some-agent';\n",
			want: 0,
		},
		{
			name: "yolo and trust-all-tools counted once each",
			src:  "exec('aider --yolo'); exec('q chat --trust-all-tools'); exec('aider --yolo');\n",
			want: 2, sev: High,
		},
		{
			name: "non-code file ignored",
			rel:  "README.md",
			src:  "run `claude --permission-mode bypassPermissions`",
			want: 0,
		},
	})
}

func TestHygienePlaintextTokenWrite(t *testing.T) {
	rule := hygFileRule(t, "plaintext_token_write")
	hygRunFileCases(t, rule, []hygFileCase{
		{
			name: "leetcode user.json",
			src:  "const user = { cookie, sessionId, csrfToken };\nfs.writeFileSync(path.join(os.homedir(), '.lc', 'user.json'), JSON.stringify(user));\n",
			want: 1, sev: High, detail: "cookie/session/csrf",
		},
		{
			name: "inline object with HOME env",
			src:  "fs.writeFile(process.env.HOME + '/.myext/auth.json', JSON.stringify({ access_token: tok, refresh_token: r }), cb);\n",
			want: 1, sev: High, detail: "access_token",
		},
		{
			name: "template with token under ~",
			src:  "await fs.outputFile('~/.config/tool/creds', `token=${apiKey}\\n`);\n",
			want: 1, sev: High,
		},
		{
			name: "python open under home",
			rel:  "scripts/login.py",
			src:  "with open(os.path.expanduser('~/.tool/session'), 'w') as f:\n    f.write(json.dumps({'jwt': jwt}))\n",
			want: 1, sev: High, detail: "jwt",
		},
		{
			name: "plain cache file",
			src:  "fs.writeFileSync(path.join(os.homedir(), '.lc', 'cache.json'), JSON.stringify({ problems, updatedAt }));\n",
			want: 0,
		},
		{
			name: "token written inside the extension's own storage dir",
			src:  "fs.writeFileSync(path.join(context.globalStorageUri.fsPath, 'state.json'), JSON.stringify({ token }));\n",
			want: 0,
		},
		{
			name: "dot-extension literal is not a dot-dir",
			src:  "fs.writeFileSync(file + '.json', JSON.stringify({ token }));\n",
			want: 0,
		},
		{
			name: "reading is not flagged",
			src:  "const { cookie, csrfToken } = JSON.parse(fs.readFileSync(path.join(os.homedir(), '.lc', 'user.json')));\n",
			want: 0,
		},
		{
			name: "python open for read",
			rel:  "a.py",
			src:  "with open(os.path.expanduser('~/.tool/session')) as f:\n    token = f.read()\n",
			want: 0,
		},
	})
}

func TestHygieneSettingsTamper(t *testing.T) {
	rule := hygFileRule(t, "settings_tamper")
	hygRunFileCases(t, rule, []hygFileCase{
		{
			name: "disable workspace trust",
			src:  "const config = vscode.workspace.getConfiguration();\nconfig.update('security.workspace.trust.enabled', false, true);\n",
			want: 1, sev: Medium, detail: "security.workspace.trust.enabled",
		},
		{
			name: "section plus key",
			src:  "vscode.workspace.getConfiguration('terminal.integrated').update('inheritEnv', true, vscode.ConfigurationTarget.Global);\n",
			want: 1, sev: Medium, detail: "terminal.integrated.inheritEnv",
		},
		{
			name: "proxy and telemetry",
			src:  "cfg.update('http.proxy', url, true);\ncfg.update('telemetry.telemetryLevel', 'all', true);\n",
			want: 2, sev: Medium, detail: "http.proxy",
		},
		{
			name: "font size is fine",
			src:  "vscode.workspace.getConfiguration('editor').update('fontSize', 14, true);\n",
			want: 0,
		},
		{
			name: "own section is fine",
			src:  "vscode.workspace.getConfiguration('leetcode').update('workspaceFolder', dir, true);\n",
			want: 0,
		},
		{
			name: "writes user settings.json",
			src:  "const p = path.join(appData, 'Code/User/settings.json');\nfs.writeFileSync(p, JSON.stringify(merged, null, 2));\n",
			want: 1, sev: Medium, detail: "Code/User/settings.json",
		},
		{
			name: "writes argv.json",
			src:  "fs.writeFileSync(path.join(os.homedir(), '.vscode', 'argv.json'), out);\n",
			want: 1, sev: Medium, detail: "argv.json",
		},
		{
			name: "writes state.vscdb",
			src:  "db.prepare('UPDATE ItemTable SET value = ? WHERE key = ?').run(v, k); // state.vscdb\n",
			want: 1, sev: Medium, detail: "state.vscdb",
		},
		{
			name: "reads settings.json only",
			src:  "const s = JSON.parse(fs.readFileSync(path.join(root, '.vscode/settings.json'), 'utf8'));\n",
			want: 0,
		},
		{
			name: "agent mcp.json belongs to agent_config_write",
			src:  "fs.writeFileSync(path.join(os.homedir(), '.claude/mcp.json'), body);\n",
			want: 0,
		},
	})
}

func TestHygieneAgentConfigWrite(t *testing.T) {
	rule := hygFileRule(t, "agent_config_write")
	hygRunFileCases(t, rule, []hygFileCase{
		{
			name: "claude settings.json",
			src:  "fs.writeFileSync(path.join(os.homedir(), '.claude', 'settings.json'), JSON.stringify(settings, null, 2));\n",
			want: 1, sev: High, detail: "~/.claude",
		},
		{
			name: "claude md is medium",
			src:  "await fs.outputFile(path.join(os.homedir(), '.claude', 'CLAUDE.md'), prompt);\n",
			want: 1, sev: Medium, detail: "steer another tool",
		},
		{
			name: "system prompt with bypass content",
			src:  "const body = JSON.stringify({ permissions: { defaultMode: 'bypassPermissions' } });\nfs.writeFileSync(`${home}/.claude/system-prompts/kfc.md`, body);\n",
			want: 1, sev: High,
		},
		{
			name: "cursor mcp",
			src:  "fs.writeFileSync(path.join(os.homedir(), '.cursor/mcp.json'), cfg);\n",
			want: 1, sev: High, detail: "~/.cursor",
		},
		{
			name: "aider conf",
			src:  "fs.appendFileSync(path.join(os.homedir(), '.aider.conf.yml'), 'yes-always: true\\n');\n",
			want: 1, sev: Medium,
		},
		{
			name: "reading claude dir is fine",
			src:  "const md = fs.readFileSync(path.join(os.homedir(), '.claude', 'CLAUDE.md'), 'utf8');\n",
			want: 0,
		},
		{
			name: "css class string is fine",
			src:  "el.querySelector('.cursor').classList.add('blink');\n",
			want: 0,
		},
	})
}
