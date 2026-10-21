package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestRenameDialogHasRoom opens the rename dialog on a pane, types a name, and
// measures the dialog on screen: 44 cells between its borders, a blank row
// above and below the field, and three cells between its two hints, the gap a
// panel's hints have. The frame is saved as rename-dialog-room when
// TUIOS_E2E_FRAMES is set.
//
// Negative control: see NEGATIVE_CONTROLS.md.
func TestRenameDialogHasRoom(t *testing.T) {
	term, base := start(t, startOpts{cols: 120, rows: 40, args: []string{"new", "e2e"}})
	killDaemon(t, base)

	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "first window")

	if err := term.SendKeys("r"); err != nil {
		t.Fatalf("open rename editor: %v", err)
	}
	if err := term.WaitFor(renameDialogUp, uiTimeout); err != nil {
		t.Fatalf("the rename dialog never opened: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("build-logs"); err != nil {
		t.Fatalf("type a name: %v", err)
	}
	if err := term.WaitForText("› build-logs", uiTimeout); err != nil {
		t.Fatalf("the field never showed the name: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "rename-dialog-room")

	s := term.Screen()
	_, rows := s.Size()
	top := -1
	for r := 0; r < rows; r++ {
		if strings.Contains(s.Line(r), "╭─ rename") {
			top = r
			break
		}
	}
	if top < 0 || top+4 >= rows {
		t.Fatalf("no rename dialog frame on screen\n%s", term.Snapshot())
	}
	frame := make([][]rune, 5)
	for i := range frame {
		frame[i] = []rune(s.Line(top + i))
	}
	left, right := -1, -1
	for x, c := range frame[0] {
		switch {
		case c == '╭' && left < 0:
			left = x
		case c == '╮' && left >= 0 && right < 0:
			right = x
		}
	}
	if inner := right - left - 1; right < 0 || inner != 44 {
		t.Fatalf("ASSERTION: the rename dialog is %d cells inside its borders, want 44\n%s", inner, term.Snapshot())
	}
	inside := func(row int) string { return string(frame[row][left+1 : right]) }
	if strings.TrimSpace(inside(1)) != "" || strings.TrimSpace(inside(3)) != "" {
		t.Fatalf("ASSERTION: the rows around the field are not blank: %q, %q\n%s", inside(1), inside(3), term.Snapshot())
	}
	if !strings.Contains(inside(2), "› build-logs") {
		t.Fatalf("ASSERTION: the field is not on the middle row: %q\n%s", inside(2), term.Snapshot())
	}
	if bottom := string(frame[4][left : right+1]); !strings.HasPrefix(bottom, "╰") || !strings.Contains(bottom, "save   ") || !strings.Contains(bottom, "   esc cancel") {
		t.Fatalf("ASSERTION: the bottom border does not hold the hints three cells apart: %q\n%s", bottom, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("cancel the rename: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !renameDialogUp(s) }, uiTimeout); err != nil {
		t.Fatalf("the rename dialog did not close: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after the rename dialog")
}
