package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/leftover"
	"github.com/shiva2021/rootout/internal/plan"
)

// Item indexes in confirmFixture.
const (
	itemFirst = iota // Application Support, bundle id match
	itemByName
	itemAdmin
	itemReceipt
	itemLogin
)

func confirmFixture() *plan.Plan {
	a := app.App{Name: "Ghostty", BundleID: "com.mitchellh.ghostty", Path: "/Applications/Ghostty.app", Cask: "ghostty"}
	items := []leftover.Leftover{
		{Path: "/Users/me/Library/Application Support/com.mitchellh.ghostty", Kind: leftover.AppSupport, MatchedBy: leftover.ByBundleID, SizeBytes: 5_000_000},
		{Path: "/Users/me/Library/Application Support/Ghostty", Kind: leftover.AppSupport, MatchedBy: leftover.ByName, SizeBytes: 2_000_000},
		{Path: "/Library/Application Support/Ghostty", Kind: leftover.AppSupport, MatchedBy: leftover.ByBundleID, SizeBytes: 1_000_000, NeedsRoot: true},
		{Path: "pkg:com.foo.pkg", Kind: leftover.Receipt, MatchedBy: leftover.ByReceipt, Note: "forgotten with pkgutil --forget"},
		{Path: a.Path, Kind: leftover.LoginItem, MatchedBy: leftover.ByLoginItem, Note: "report only; remove in System Settings > General > Login Items"},
	}
	p := &plan.Plan{App: a, Mode: plan.Trash, BrewZap: true}
	for _, l := range items {
		p.Items = append(p.Items, plan.Item{Leftover: l, Selected: true})
	}
	return p
}

func newTestConfirm(t *testing.T) (*Confirm, *plan.Plan) {
	t.Helper()
	p := confirmFixture()
	c := NewConfirm(p)
	c.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return c, p
}

func pressC(c *Confirm, keys ...tea.KeyPressMsg) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		_, cmd = c.Update(k)
	}
	return cmd
}

func viewC(c *Confirm) string { return plain(c.View().Content) }

var (
	keySpace = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	keyDown  = tea.KeyPressMsg{Code: tea.KeyDown}
	keyUp    = tea.KeyPressMsg{Code: tea.KeyUp}
	keyEnter = tea.KeyPressMsg{Code: tea.KeyEnter}
	keyEsc   = tea.KeyPressMsg{Code: tea.KeyEscape}
	keyA     = tea.KeyPressMsg{Code: 'a', Text: "a"}
	keyN     = tea.KeyPressMsg{Code: 'n', Text: "n"}
)

func TestConfirmView(t *testing.T) {
	c, _ := newTestConfirm(t)
	v := viewC(c)
	for _, want := range []string{
		"Remove Ghostty (com.mitchellh.ghostty)",
		"→ Trash",
		"[x] brew uninstall --zap ghostty first",
		"Application Support",
		"Receipt",
		"Login item",
		"[admin]",
		"[name]",
		"pkg:com.foo.pkg",
		"report only; remove in System Settings",
		"3 paths · 8 MB · 1 need admin (deleted permanently)",
		confirmHelp,
	} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q:\n%s", want, v)
		}
	}
	if !strings.Contains(v, "5 MB") {
		t.Errorf("view missing item size:\n%s", v)
	}
}

func TestConfirmModeBadges(t *testing.T) {
	for _, tc := range []struct {
		mode plan.Mode
		want string
	}{
		{plan.Trash, "→ Trash"},
		{plan.Permanent, "PERMANENT"},
		{plan.DryRun, "DRY RUN"},
	} {
		t.Run(tc.mode.String(), func(t *testing.T) {
			p := confirmFixture()
			p.Mode = tc.mode
			if v := viewC(NewConfirm(p)); !strings.Contains(v, tc.want) {
				t.Errorf("view missing badge %q:\n%s", tc.want, v)
			}
		})
	}
}

func TestConfirmBrewToggle(t *testing.T) {
	c, p := newTestConfirm(t)
	if c.cursorItem() != -1 || c.rows[c.cursor].kind != rowBrew {
		t.Fatalf("cursor should start on the brew row, got row %d", c.cursor)
	}
	pressC(c, keySpace)
	if p.BrewZap {
		t.Error("space on the brew row should turn BrewZap off")
	}
	if v := viewC(c); !strings.Contains(v, "[ ] brew uninstall --zap ghostty first") {
		t.Errorf("view should show the brew row unchecked:\n%s", v)
	}
	pressC(c, keySpace)
	if !p.BrewZap {
		t.Error("second space should turn BrewZap back on")
	}
}

func TestConfirmItemToggle(t *testing.T) {
	c, p := newTestConfirm(t)
	pressC(c, keyDown)
	if got := c.cursorItem(); got != itemFirst {
		t.Fatalf("cursor item after down = %d, want %d", got, itemFirst)
	}
	pressC(c, keySpace)
	if p.Items[itemFirst].Selected {
		t.Error("space should deselect the first item")
	}
	if n, _, _ := p.Totals(); n != 2 {
		t.Errorf("Totals count = %d after deselect, want 2", n)
	}
	if v := viewC(c); !strings.Contains(v, "2 paths · 3 MB") {
		t.Errorf("totals line not updated:\n%s", v)
	}
	if p.BrewZap != true {
		t.Error("toggling an item must not touch BrewZap")
	}
}

func TestConfirmNoneThenEnterFlashes(t *testing.T) {
	c, p := newTestConfirm(t)
	pressC(c, keySpace) // brew off
	pressC(c, keyN)
	for i, it := range p.Items {
		if it.Kind == leftover.LoginItem {
			if !it.Selected {
				t.Error("n must not touch the login item")
			}
			continue
		}
		if it.Selected {
			t.Errorf("item %d still selected after n", i)
		}
	}
	cmd := pressC(c, keyEnter)
	if cmd != nil && isQuit(cmd) {
		t.Fatal("enter with nothing selected must not quit")
	}
	if c.Accepted() {
		t.Error("Accepted() should be false")
	}
	if v := viewC(c); !strings.Contains(v, nothingFlash) || !strings.Contains(v, "0 paths · 0 B") {
		t.Errorf("flash or empty totals missing:\n%s", v)
	}
}

func TestConfirmAllThenEnterAccepts(t *testing.T) {
	c, p := newTestConfirm(t)
	pressC(c, keyN, keyA)
	for i, it := range p.Items {
		if !it.Selected {
			t.Errorf("item %d not selected after a", i)
		}
	}
	assertQuit(t, pressC(c, keyEnter))
	if !c.Accepted() {
		t.Error("Accepted() should be true after enter")
	}
}

func TestConfirmBrewOnlyAccepts(t *testing.T) {
	c, _ := newTestConfirm(t)
	pressC(c, keyN)
	assertQuit(t, pressC(c, keyEnter))
	if !c.Accepted() {
		t.Error("brew zap alone is enough to accept")
	}
}

func TestConfirmCancel(t *testing.T) {
	for name, key := range map[string]tea.KeyPressMsg{
		"esc":    keyEsc,
		"q":      {Code: 'q', Text: "q"},
		"ctrl+c": {Code: 'c', Mod: tea.ModCtrl},
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := newTestConfirm(t)
			assertQuit(t, pressC(c, key))
			if c.Accepted() {
				t.Error("Accepted() should be false after cancel")
			}
		})
	}
}

func TestConfirmNavigationSkipsLoginItem(t *testing.T) {
	c, _ := newTestConfirm(t)
	seen := map[int]bool{}
	for i := 0; i < 20; i++ {
		pressC(c, keyDown)
		if got := c.cursorItem(); got == itemLogin {
			t.Fatal("cursor landed on the login item")
		} else if got >= 0 {
			seen[got] = true
		}
	}
	if got := c.cursorItem(); got != itemReceipt {
		t.Errorf("cursor should stop on the receipt (last toggleable), got %d", got)
	}
	for _, want := range []int{itemFirst, itemByName, itemAdmin, itemReceipt} {
		if !seen[want] {
			t.Errorf("navigation never visited item %d", want)
		}
	}
	for i := 0; i < 20; i++ {
		pressC(c, keyUp)
		if c.cursorItem() == itemLogin {
			t.Fatal("cursor landed on the login item going up")
		}
	}
	if c.rows[c.cursor].kind != rowBrew {
		t.Error("up should stop on the brew row")
	}
	pressC(c, tea.KeyPressMsg{Code: tea.KeyPgDown})
	if got := c.cursorItem(); got != itemReceipt {
		t.Errorf("pgdown should reach the receipt, got %d", got)
	}
	pressC(c, tea.KeyPressMsg{Code: tea.KeyPgUp})
	if c.rows[c.cursor].kind != rowBrew {
		t.Error("pgup should return to the brew row")
	}
}

func TestConfirmNoCaskHasNoBrewRow(t *testing.T) {
	p := confirmFixture()
	p.App.Cask = ""
	c := NewConfirm(p)
	c.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if strings.Contains(viewC(c), "brew uninstall") {
		t.Error("brew row shown without a cask")
	}
	if got := c.cursorItem(); got != itemFirst {
		t.Errorf("cursor should start on the first item, got %d", got)
	}
}

func TestConfirmSmallHeightScrolls(t *testing.T) {
	c, _ := newTestConfirm(t)
	c.Update(tea.WindowSizeMsg{Width: 60, Height: 8})
	for i := 0; i < 10; i++ {
		pressC(c, keyDown)
	}
	v := viewC(c)
	if !strings.Contains(v, "pkg:com.foo.pkg") {
		t.Errorf("list did not scroll to the cursor row:\n%s", v)
	}
	if !strings.Contains(v, confirmHelp) || !strings.Contains(v, "Remove Ghostty") {
		t.Errorf("header or footer lost at small height:\n%s", v)
	}
	if lines := strings.Count(v, "\n") + 1; lines != 8 {
		t.Errorf("view has %d lines at height 8, want 8:\n%s", lines, v)
	}
}
