package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func newTestTyped(t *testing.T) typedConfirm {
	t.Helper()
	tc, _ := newTypedConfirm("delete", "Type delete to move 7 paths to the Trash", false)
	if !tc.Focused() {
		t.Fatal("input not focused on creation")
	}
	return tc
}

// typeInto feeds s one key press at a time.
func typeInto(tc typedConfirm, s string) typedConfirm {
	for _, r := range s {
		code := r
		if r == ' ' {
			code = tea.KeySpace
		}
		tc, _, _, _ = tc.Update(tea.KeyPressMsg{Code: code, Text: string(r)})
	}
	return tc
}

func TestTypedConfirmAccepts(t *testing.T) {
	for _, in := range []string{"delete", "DELETE", " delete ", "Delete"} {
		t.Run(in, func(t *testing.T) {
			tc := typeInto(newTestTyped(t), in)
			tc, _, ok, cancel := tc.Update(keyEnter)
			if !ok || cancel {
				t.Fatalf("%q: accepted=%v cancelled=%v, want accepted", in, ok, cancel)
			}
			if tc.err != "" {
				t.Fatalf("%q: unexpected err %q", in, tc.err)
			}
		})
	}
}

func TestTypedConfirmRejects(t *testing.T) {
	for _, in := range []string{"del", "delete now", ""} {
		t.Run(in, func(t *testing.T) {
			tc := typeInto(newTestTyped(t), in)
			tc, _, ok, cancel := tc.Update(keyEnter)
			if ok || cancel {
				t.Fatalf("%q: accepted=%v cancelled=%v, want neither", in, ok, cancel)
			}
			if tc.err != typedWrongErr {
				t.Fatalf("%q: err = %q, want %q", in, tc.err, typedWrongErr)
			}
			if tc.input.Value() != in {
				t.Fatalf("%q: input value changed to %q", in, tc.input.Value())
			}
			if v := tc.View(80); !strings.Contains(v, typedWrongErr) {
				t.Fatalf("View missing err flash:\n%s", v)
			}
			// Typing again clears the flash.
			tc = typeInto(tc, "x")
			if tc.err != "" {
				t.Fatalf("err not cleared after typing: %q", tc.err)
			}
		})
	}
}

func TestTypedConfirmCancels(t *testing.T) {
	for name, key := range map[string]tea.KeyPressMsg{
		"esc":    keyEsc,
		"ctrl+c": {Code: 'c', Mod: tea.ModCtrl},
	} {
		t.Run(name, func(t *testing.T) {
			tc := typeInto(newTestTyped(t), "del")
			_, _, ok, cancel := tc.Update(key)
			if ok || !cancel {
				t.Fatalf("accepted=%v cancelled=%v, want cancelled", ok, cancel)
			}
		})
	}
}

func TestTypedConfirmPasteNeedsEnter(t *testing.T) {
	tc := newTestTyped(t)
	tc, _, ok, cancel := tc.Update(tea.PasteMsg{Content: "delete\n"})
	if ok || cancel {
		t.Fatalf("paste alone accepted=%v cancelled=%v", ok, cancel)
	}
	if got := tc.input.Value(); got != "delete" {
		t.Fatalf("pasted value = %q, want newline stripped", got)
	}
	if _, _, ok, _ := tc.Update(keyEnter); !ok {
		t.Fatal("enter after paste should accept")
	}
}

func TestTypedConfirmView(t *testing.T) {
	tc := typeInto(newTestTyped(t), "del")
	for _, width := range []int{80, 60, 40} {
		v := tc.View(width)
		for _, want := range []string{"Type delete to move 7 paths", "del", typedHelp, "╭", "╰"} {
			if !strings.Contains(v, want) {
				t.Fatalf("View(%d) missing %q:\n%s", width, want, v)
			}
		}
		if w := lipgloss.Width(v); w > min(width, typedBoxWidth) {
			t.Fatalf("View(%d) is %d wide:\n%s", width, w, v)
		}
	}
	danger, _ := newTypedConfirm("delete", "Type delete to permanently remove 3 paths", true)
	if v := danger.View(80); !strings.Contains(v, "permanently") {
		t.Fatalf("danger view:\n%s", v)
	}
}
