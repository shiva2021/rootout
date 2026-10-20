package heur

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shiva2021/rootout/internal/ext"
)

func TestRescoreAddsMarketplaceFindingsOnce(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "extension.js"), []byte("module.exports = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const name = "TestRescoreAddsMarketplaceFindingsOnce"
	RegisterExt(ExtRule{Name: "test_rescore_online", Check: func(c *ExtCtx) []Finding {
		if c.Ext.Name != name || c.Marketplace == nil || c.Marketplace.Verified {
			return nil
		}
		return []Finding{{Severity: High, Detail: "unverified"}}
	}})
	RegisterExt(ExtRule{Name: "test_rescore_offline", Check: func(c *ExtCtx) []Finding {
		if c.Ext.Name != name {
			return nil
		}
		return []Finding{{Severity: Low, Detail: "always"}}
	}})
	e := ext.Extension{Name: name, Publisher: "x", Dir: dir}
	r, err := Scan(context.Background(), e, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	before := r.Score
	if count(r, "test_rescore_offline") != 1 || count(r, "test_rescore_online") != 0 {
		t.Fatalf("offline findings = %+v", r.Findings)
	}
	r2 := Rescore(context.Background(), r, &Marketplace{Verified: false})
	if count(r2, "test_rescore_online") != 1 || count(r2, "test_rescore_offline") != 1 {
		t.Fatalf("rescored findings = %+v", r2.Findings)
	}
	if r2.Score <= before {
		t.Fatalf("score did not rise: %d -> %d", before, r2.Score)
	}
	if r3 := Rescore(context.Background(), r2, &Marketplace{Verified: false}); len(r3.Findings) != len(r2.Findings) || r3.Score != r2.Score {
		t.Fatalf("rescore is not idempotent: %d findings, score %d", len(r3.Findings), r3.Score)
	}
	if r4 := Rescore(context.Background(), r, nil); r4.Score != before {
		t.Fatal("nil metadata must be a no-op")
	}
}

func count(r Report, rule string) int {
	n := 0
	for _, f := range r.Findings {
		if f.Rule == rule {
			n++
		}
	}
	return n
}
