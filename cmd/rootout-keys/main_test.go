package main

import (
	"bytes"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shiva2021/rootout/internal/license"
)

const testMID = "0123456789abcdef0123456789abcdef"

func runCmd(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errw bytes.Buffer
	code = run(args, strings.NewReader(""), &out, &errw)
	return code, out.String(), errw.String()
}

func genKey(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "keys", "private.pem")
	if code, _, errs := runCmd(t, "gen", "--out", path); code != 0 {
		t.Fatalf("gen exit %d: %s", code, errs)
	}
	return path
}

func pubFor(t *testing.T, path string) ed25519.PublicKey {
	t.Helper()
	priv, err := loadKey(path)
	if err != nil {
		t.Fatal(err)
	}
	return priv.Public().(ed25519.PublicKey)
}

func TestGenWritesPEMAndPrintsKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "private.pem")
	code, out, errs := runCmd(t, "gen", "--out", path)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("key mode = %o, want 600", fi.Mode().Perm())
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %o, want 700", di.Mode().Perm())
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %q", out)
	}
	hexPart, ok := strings.CutPrefix(lines[0], "public key hex: ")
	if !ok || len(hexPart) != 64 {
		t.Errorf("bad public key line %q", lines[0])
	}
	b64Part, ok := strings.CutPrefix(lines[1], "worker secret (PKCS8 base64): ")
	if !ok || b64Part == "" {
		t.Errorf("bad secret line %q", lines[1])
	}
	if hexPart != hexOf(pubFor(t, path)) {
		t.Error("printed hex does not match the key file")
	}
}

func TestGenRefusesOverwrite(t *testing.T) {
	path := genKey(t)
	before, _ := os.ReadFile(path)
	code, _, errs := runCmd(t, "gen", "--out", path)
	if code == 0 || !strings.Contains(errs, "exists") {
		t.Fatalf("want failure mentioning exists, got %d %q", code, errs)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Error("existing key was modified")
	}
}

func TestAdminKeyDecodes(t *testing.T) {
	path := genKey(t)
	code, out, errs := runCmd(t, "admin", "--mid", testMID, "--key", path)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	c, err := license.Decode(strings.TrimSpace(out), pubFor(t, path))
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind != license.KindAdmin || c.MID != testMID || c.EXP != 0 || c.Sub != "admin" {
		t.Errorf("claims = %+v", c)
	}
}

func TestSignPatronDays(t *testing.T) {
	path := genKey(t)
	code, out, errs := runCmd(t, "sign", "--kind", "patron", "--sub", "u1", "--mid", testMID, "--days", "35", "--key", path)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	c, err := license.Decode(strings.TrimSpace(out), pubFor(t, path))
	if err != nil {
		t.Fatal(err)
	}
	if got := time.Duration(c.EXP-c.IAT) * time.Second; got != 35*24*time.Hour {
		t.Errorf("lifetime = %v", got)
	}
	if c.Kind != license.KindPatron || c.Sub != "u1" {
		t.Errorf("claims = %+v", c)
	}
}

func TestSignRejectsBadMID(t *testing.T) {
	path := genKey(t)
	for _, mid := range []string{"short", strings.ToUpper(testMID), ""} {
		code, out, _ := runCmd(t, "sign", "--kind", "trial", "--sub", "u1", "--mid", mid, "--days", "7", "--key", path)
		if code != 2 || out != "" {
			t.Errorf("mid %q: exit %d stdout %q, want 2 and no output", mid, code, out)
		}
	}
}

func TestSignRejectsBadKindAndExpiredDays(t *testing.T) {
	path := genKey(t)
	if code, _, _ := runCmd(t, "sign", "--kind", "admin", "--sub", "u", "--mid", testMID, "--days", "1", "--key", path); code != 2 {
		t.Errorf("admin kind via sign: exit %d, want 2", code)
	}
	if code, _, _ := runCmd(t, "sign", "--kind", "trial", "--sub", "u", "--mid", testMID, "--days", "-1", "--key", path); code != 2 {
		t.Errorf("negative days: exit %d, want 2", code)
	}
}

func TestInspectPrintsClaims(t *testing.T) {
	path := genKey(t)
	_, key, _ := runCmd(t, "admin", "--mid", testMID, "--key", path)
	code, out, errs := runCmd(t, "inspect", "--key", path, strings.TrimSpace(key))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.Contains(out, `"kind": "admin"`) || !strings.Contains(out, testMID) {
		t.Errorf("inspect output = %q", out)
	}
	if code, _, _ := runCmd(t, "inspect", "--key", path, "ro1.bad.bad"); code != 2 {
		t.Errorf("bad key: exit %d, want 2", code)
	}
}

func TestExitCodes(t *testing.T) {
	path := genKey(t)
	missing := filepath.Join(t.TempDir(), "none.pem")
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"unknown flag", []string{"admin", "--bogus"}, 2},
		{"unknown command", []string{"bogus"}, 2},
		{"missing mid", []string{"admin", "--key", path}, 2},
		{"missing sign flags", []string{"sign", "--kind", "trial", "--key", path}, 2},
		{"non-integer days", []string{"sign", "--kind", "trial", "--sub", "u", "--mid", testMID, "--days", "x", "--key", path}, 2},
		{"extra args", []string{"gen", "extra", "--out", missing}, 2},
		{"inspect needs a key", []string{"inspect", "--key", path}, 2},
		{"unreadable key file", []string{"admin", "--mid", testMID, "--key", missing}, 1},
		{"ok", []string{"admin", "--mid", testMID, "--key", path}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if code, _, errs := runCmd(t, tt.args...); code != tt.want {
				t.Errorf("exit %d, want %d (stderr %q)", code, tt.want, errs)
			}
		})
	}
}

func TestGenEmptyPathNamesOutFlag(t *testing.T) {
	code, _, errs := runCmd(t, "gen", "--out", "")
	if code != 2 || !strings.Contains(errs, "pass --out") {
		t.Fatalf("exit %d, stderr %q; want exit 2 naming --out", code, errs)
	}
}
