package plan

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/shiva2021/rootout/internal/leftover"
	"github.com/shiva2021/rootout/internal/progress"
	"github.com/shiva2021/rootout/internal/testutil"
)

func TestExecuteProgressTrash(t *testing.T) {
	env, p := buildWithRoot(t)
	rec := &recorder{}
	prog := &progress.Recorder{}
	d := deps(rec)
	d.Progress = prog
	if _, err := Execute(context.Background(), env, p, d); err != nil {
		t.Fatal(err)
	}

	var userPaths []string
	for _, it := range p.Selected() {
		if isPath(it.Leftover) && !it.NeedsRoot {
			userPaths = append(userPaths, it.Path)
		}
	}
	want := append(append([]string{}, userPaths...), "admin rights")
	if prog.Label != "Removing" || !prog.Finished {
		t.Errorf("label %q finished %v", prog.Label, prog.Finished)
	}
	if prog.Total != len(want) {
		t.Errorf("Total = %d, want %d (%d user paths + admin)", prog.Total, len(want), len(userPaths))
	}
	if strings.Join(prog.Steps, "\n") != strings.Join(want, "\n") {
		t.Errorf("Steps =\n%s\nwant\n%s", strings.Join(prog.Steps, "\n"), strings.Join(want, "\n"))
	}
}

func TestExecuteProgressPermanent(t *testing.T) {
	env, p := buildWithRoot(t)
	p.Mode = Permanent
	rec := &recorder{}
	prog := &progress.Recorder{}
	d := deps(rec)
	d.Progress = prog
	d.RemoveAll = func(string) error { return nil }
	if _, err := Execute(context.Background(), env, p, d); err != nil {
		t.Fatal(err)
	}
	if prog.Total != 6 || len(prog.Steps) != 6 || prog.Steps[5] != "admin rights" || !prog.Finished {
		t.Fatalf("total %d steps %v finished %v", prog.Total, prog.Steps, prog.Finished)
	}
}

func TestExecuteProgressBatches(t *testing.T) {
	const n = 60
	files := map[string]string{}
	for i := range n {
		files[fmt.Sprintf("~/Library/Caches/%s.h%02d/a.db", spotifyID, i)] = "x"
	}
	env := testutil.FakeHome(t, testutil.Spec{Files: files})
	var items []Item
	for i := range n {
		items = append(items, Item{
			Leftover: leftover.Leftover{Path: env.Library("Caches", fmt.Sprintf("%s.h%02d", spotifyID, i)), Kind: leftover.Cache, MatchedBy: leftover.ByBundlePrefix},
			Selected: true,
		})
	}
	p := &Plan{Items: items, Mode: Trash}

	rec := &recorder{}
	prog := &progress.Recorder{}
	d := deps(rec)
	d.Progress = prog
	res, err := Execute(context.Background(), env, p, d)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() || len(res.Removed) != n {
		t.Fatalf("result = %+v", res)
	}
	batches := 0
	for _, c := range rec.calls {
		if strings.Contains(c, "/usr/bin/trash") {
			batches++
		}
	}
	if batches != 3 {
		t.Errorf("trash batches = %d, want 3 (25+25+10)", batches)
	}
	if prog.Total != n || len(prog.Steps) != n || !prog.Finished {
		t.Errorf("total %d steps %d finished %v", prog.Total, len(prog.Steps), prog.Finished)
	}
	for i, it := range items {
		if prog.Steps[i] != it.Path {
			t.Fatalf("step %d = %q, want %q", i, prog.Steps[i], it.Path)
		}
	}
	if rec.has("administrator privileges") >= 0 {
		t.Error("admin step run with no root work")
	}
}
