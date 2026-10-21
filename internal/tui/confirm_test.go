package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/leftover"
	"github.com/shiva2021/rootout/internal/output"
	"github.com/shiva2021/rootout/internal/plan"
	"github.com/shiva2021/rootout/internal/tcc"
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
	assertQuit(t, acceptTyped(t, c))
	if !c.Accepted() {
		t.Error("Accepted() should be true after typing delete")
	}
}

func TestConfirmBrewOnlyAccepts(t *testing.T) {
	c, _ := newTestConfirm(t)
	pressC(c, keyN)
	assertQuit(t, acceptTyped(t, c))
	if !c.Accepted() {
		t.Error("brew zap alone is enough to accept")
	}
}

func TestConfirmCancel(t *testing.T) {
	for name, tc := range map[string]struct {
		key     tea.KeyPressMsg
		quitAll bool
	}{
		"esc":    {keyEsc, false},
		"q":      {tea.KeyPressMsg{Code: 'q', Text: "q"}, false},
		"ctrl+c": {tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, true},
		"ctrl+x": {tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl}, true},
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := newTestConfirm(t)
			assertQuit(t, pressC(c, tc.key))
			if c.Accepted() {
				t.Error("Accepted() should be false after cancel")
			}
			if c.quitAll != tc.quitAll {
				t.Errorf("quitAll = %v, want %v", c.quitAll, tc.quitAll)
			}
		})
	}
}

func TestConfirmCtrlXFromTypedQuitsAll(t *testing.T) {
	c, _ := newTestConfirm(t)
	pressC(c, keyEnter)
	if !c.typing {
		t.Fatal("enter should open the typed box")
	}
	pressC(c, tea.KeyPressMsg{Code: 'd', Text: "d"}, tea.KeyPressMsg{Code: 'e', Text: "e"})
	assertQuit(t, pressC(c, tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl}))
	if !c.quitAll || c.Accepted() {
		t.Errorf("quitAll=%v accepted=%v, want true/false", c.quitAll, c.Accepted())
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

// typeC feeds s one key press at a time.
func typeC(c *Confirm, s string) {
	for _, r := range s {
		pressC(c, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// acceptTyped presses Enter, types the confirm word and presses Enter again,
// returning the final command.
func acceptTyped(t *testing.T, c *Confirm) tea.Cmd {
	t.Helper()
	pressC(c, keyEnter)
	if !c.typing {
		t.Fatal("enter should open the typed box")
	}
	typeC(c, plan.ConfirmWord)
	return pressC(c, keyEnter)
}

func TestConfirmEnterOpensTypedBox(t *testing.T) {
	c, _ := newTestConfirm(t)
	cmd := pressC(c, keyEnter)
	if cmd != nil && isQuit(cmd) {
		t.Fatal("enter alone must not quit")
	}
	if c.Accepted() {
		t.Fatal("enter alone must not accept")
	}
	if want := "Type delete to move 3 paths to the Trash and run brew uninstall --zap ghostty; 1 need admin rights"; c.typed.prompt != want {
		t.Errorf("prompt = %q, want %q", c.typed.prompt, want)
	}
	v := viewC(c)
	for _, want := range []string{
		"Type delete to move 3 paths", typedHelp, "╭", "Remove Ghostty", // the list stays behind the box
	} {
		if !strings.Contains(v, want) {
			t.Errorf("typed view missing %q:\n%s", want, v)
		}
	}
	if lines := strings.Count(v, "\n") + 1; lines != 30 {
		t.Errorf("view has %d lines, want 30", lines)
	}
}

func TestConfirmYDoesNotAccept(t *testing.T) {
	c, _ := newTestConfirm(t)
	// On the list y is ignored...
	if cmd := pressC(c, tea.KeyPressMsg{Code: 'y', Text: "y"}); cmd != nil && isQuit(cmd) {
		t.Fatal("y on the list must not quit")
	}
	// ...and in the typed box it is the wrong word.
	pressC(c, keyEnter)
	typeC(c, "y")
	if cmd := pressC(c, keyEnter); cmd != nil && isQuit(cmd) {
		t.Fatal("y + enter must not quit")
	}
	if c.Accepted() {
		t.Fatal("y must not accept")
	}
	if v := viewC(c); !strings.Contains(v, typedWrongErr) {
		t.Errorf("wrong-word flash missing:\n%s", v)
	}
}

func TestConfirmTypedDeleteAccepts(t *testing.T) {
	c, _ := newTestConfirm(t)
	assertQuit(t, acceptTyped(t, c))
	if !c.Accepted() {
		t.Error("Accepted() should be true after typing delete")
	}
}

func TestConfirmEscFromTypedReturnsToList(t *testing.T) {
	c, p := newTestConfirm(t)
	pressC(c, keyEnter)
	typeC(c, "del")
	cmd := pressC(c, keyEsc)
	if cmd != nil && isQuit(cmd) {
		t.Fatal("esc in the typed box must not quit the screen")
	}
	if c.typing || c.Accepted() {
		t.Fatalf("typing=%v accepted=%v after esc, want back on the list", c.typing, c.Accepted())
	}
	if v := viewC(c); strings.Contains(v, "Type delete") {
		t.Errorf("typed box still visible:\n%s", v)
	}
	// Keys work on the list again; the selection was not touched.
	pressC(c, keySpace)
	if p.BrewZap {
		t.Error("space after esc should toggle the brew row")
	}
	assertQuit(t, pressC(c, keyEsc))
}

func TestConfirmPermanentPromptIsDanger(t *testing.T) {
	p := confirmFixture()
	p.Mode = plan.Permanent
	p.App.Cask = ""
	c := NewConfirm(p)
	c.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	pressC(c, keyEnter)
	if !c.typed.danger {
		t.Error("permanent plan should use the danger box")
	}
	if want := "Type delete to permanently delete 3 paths; 1 need admin rights"; c.typed.prompt != want {
		t.Errorf("prompt = %q, want %q", c.typed.prompt, want)
	}
	if v := viewC(c); !strings.Contains(v, "permanently delete 3 paths") {
		t.Errorf("permanent prompt missing:\n%s", v)
	}
}

func groupedFixture() *plan.Plan {
	items := []plan.Item{
		{Leftover: leftover.Leftover{Path: "/Users/me/Library/Caches/com.old.app", Kind: leftover.Cache, MatchedBy: leftover.ByBundleID, SizeBytes: 1_500}, Group: "com.old.app"},
		{Leftover: leftover.Leftover{Path: "/Users/me/Library/Preferences/com.old.app.plist", Kind: leftover.Preferences, MatchedBy: leftover.ByBundleID, SizeBytes: 500, Note: "recently used (changed 3 days ago)"}, Group: "com.old.app"},
		{Leftover: leftover.Leftover{Path: "/Users/me/Library/Application Support/com.gone.tool", Kind: leftover.AppSupport, MatchedBy: leftover.ByBundleID, SizeBytes: 2_000}, Group: "com.gone.tool"},
	}
	return plan.New("Orphaned leftovers", items, plan.Trash, true)
}

func TestConfirmGroupedPlan(t *testing.T) {
	c := NewConfirm(groupedFixture())
	c.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	v := viewC(c)
	for _, want := range []string{
		"Orphaned leftovers", "→ Trash", "com.old.app", "com.gone.tool",
		"[Cache]", "[Preferences]", "[Application Support]",
		recentTag,
		tickHint,
		"0 paths · 0 B",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("grouped view missing %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "Remove ") {
		t.Errorf("label plan must not show a Remove title:\n%s", v)
	}
	var headings []string
	for _, r := range c.rows {
		if r.kind == rowHeading {
			headings = append(headings, r.text)
		}
	}
	if got := strings.Join(headings, ","); got != "com.old.app,com.gone.tool" {
		t.Errorf("group headings = %q, want com.old.app,com.gone.tool", got)
	}
	if strings.Count(v, "[ ] ") != 3 || strings.Contains(v, "[x] ") {
		t.Errorf("StartUnselected plan should begin with nothing ticked:\n%s", v)
	}
	if c.cursorItem() != 0 {
		t.Fatalf("cursor should start on the first item, got %d", c.cursorItem())
	}
	// Enter with nothing ticked flashes; ticking one row removes the hint.
	pressC(c, keyEnter)
	if c.typing {
		t.Fatal("enter with nothing ticked must not open the typed box")
	}
	pressC(c, keySpace)
	v = viewC(c)
	if strings.Contains(v, tickHint) || !strings.Contains(v, "1 paths · "+output.Bytes(1_500)) {
		t.Errorf("after ticking, hint should go and totals update:\n%s", v)
	}
	pressC(c, keyEnter)
	if v := viewC(c); !strings.Contains(v, "Type delete to move 1 path to the Trash") {
		t.Errorf("typed prompt for one path:\n%s", v)
	}
}

func TestConfirmRecentTagOnlyWhenNoted(t *testing.T) {
	c, _ := newTestConfirm(t)
	if v := viewC(c); strings.Contains(v, recentTag) {
		t.Fatalf("app plan has no recent items:\n%s", v)
	}
	p := confirmFixture()
	p.Items[itemByName].Note = "Recently used: changed yesterday"
	if v := viewC(NewConfirm(p)); !strings.Contains(v, recentTag) {
		t.Fatalf("recent tag missing:\n%s", v)
	}
}

func TestConfirmHelpOverlay(t *testing.T) {
	c, p := newTestConfirm(t)
	if !strings.Contains(viewC(c), "? help") {
		t.Fatal("footer should offer ? help")
	}
	pressC(c, tea.KeyPressMsg{Code: '?', Text: "?"})
	v := viewC(c)
	for _, want := range []string{"Keys", "space", "pgup/pgdn", "type delete", "ctrl+x", helpClose} {
		if !strings.Contains(v, want) {
			t.Fatalf("help overlay missing %q:\n%s", want, v)
		}
	}
	// Any key closes it without acting.
	cmd := pressC(c, keySpace)
	if cmd != nil && isQuit(cmd) {
		t.Fatal("closing key must not act")
	}
	if !p.BrewZap {
		t.Error("closing key must be consumed, not toggle the brew row")
	}
	if strings.Contains(viewC(c), helpClose) {
		t.Fatal("overlay still open")
	}
}

func TestConfirmShowsFullDiskAccessWarning(t *testing.T) {
	p := confirmFixture()
	p.Items = append(p.Items, plan.Item{Leftover: leftover.Leftover{
		Path: "/Users/me/Library/Containers/com.mitchellh.ghostty", Kind: leftover.Container,
		MatchedBy: leftover.ByBundleID, SizeBytes: 1_000_000, Note: tcc.Note,
	}, Selected: true})
	p.Warning = "1 path needs Full Disk Access; grant it to Warp in " + tcc.SettingsPath + " or it will fail"
	c := NewConfirm(p)
	c.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	v := viewC(c)
	for _, want := range []string{fdaTag, p.Warning} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q:\n%s", want, v)
		}
	}
	// A flash takes the same line and wins while it is showing.
	p.BrewZap = false
	pressC(c, keyN, keyEnter)
	if v := viewC(c); !strings.Contains(v, nothingFlash) || strings.Contains(v, p.Warning) {
		t.Errorf("flash must replace the warning:\n%s", v)
	}
}
