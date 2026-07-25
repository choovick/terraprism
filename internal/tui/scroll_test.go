package tui

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/CaptShanks/terraprism/internal/tfplan"
)

func manyResourcesPlan(n int) *tfplan.Plan {
	resources := make([]tfplan.Resource, n)
	for i := range resources {
		resources[i] = tfplan.Resource{
			Address:    fmt.Sprintf("null_resource.r%d", i),
			Action:     tfplan.ActionUpdate,
			Attributes: []tfplan.Attribute{leaf("x", tfplan.ActionUpdate, tfplan.KindNumber, "1", "2")},
		}
	}
	return &tfplan.Plan{Resources: resources}
}

// Regression: scrolling far away with the mouse wheel while the cursor
// sits on the first (or last) item, then pressing the boundary key that
// can't move the cursor further (k at the top, j at the bottom), used to
// nudge the viewport back by only one line per keypress — since that
// branch never called ensureCursorVisible, it took as many keypresses as
// the scroll distance to bring the cursor back into view. It should snap
// back immediately instead, exactly like moving the cursor does.
func TestBoundaryKeyAfterMouseScrollSnapsBackImmediately(t *testing.T) {
	tests := []struct {
		name        string
		wheelButton tea.MouseButton
		boundaryKey string
	}{
		{name: "top boundary after scrolling down", wheelButton: tea.MouseButtonWheelDown, boundaryKey: "k"},
		{name: "bottom boundary after scrolling up", wheelButton: tea.MouseButtonWheelUp, boundaryKey: "j"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := manyResourcesPlan(40)
			m := NewModel(plan, "")
			model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
			mm := model.(Model)

			if tc.boundaryKey == "j" {
				// Move the cursor to the last item first, so 'j' is the
				// boundary key (can't move further down).
				for i := 0; i < len(plan.Resources)-1; i++ {
					model, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
					mm = model.(Model)
				}
			}

			for i := 0; i < 15; i++ {
				model, _ = mm.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tc.wheelButton})
				mm = model.(Model)
			}
			scrolledOffset := mm.viewport.YOffset
			if mm.cursorLineVisible() {
				t.Fatalf("expected the mouse wheel to scroll the cursor's row out of view, but it's still visible at YOffset=%d", scrolledOffset)
			}

			model, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tc.boundaryKey)})
			mm = model.(Model)

			topLine := mm.viewport.YOffset
			bottomLine := topLine + mm.viewport.Height - 1
			if mm.selectedLineStart < topLine || mm.selectedLineStart > bottomLine {
				t.Fatalf("cursor should be visible after a single %q press (was scrolled to %d), got viewport=[%d,%d] selectedLineStart=%d",
					tc.boundaryKey, scrolledOffset, topLine, bottomLine, mm.selectedLineStart)
			}
		})
	}
}

// Free-scrolling past the boundary must still work when the viewport is
// already where the cursor is (no mouse drift to correct for) — this is
// the behavior the boundary branches exist for in the first place.
// Reaching the last item via 'j' leaves its row at the exact bottom edge
// of the viewport (ensureCursorVisible aligns it there); one more 'j' at
// that point is the "already at cursor" case, not a mouse-drift case, so
// it should free-scroll by exactly one more line rather than snap.
func TestBoundaryKeyFreeScrollsWhenAlreadyAtCursor(t *testing.T) {
	plan := manyResourcesPlan(40)
	m := NewModel(plan, "")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	mm := model.(Model)

	for i := 0; i < len(plan.Resources)-1; i++ {
		model, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		mm = model.(Model)
	}
	if !mm.cursorLineVisible() {
		t.Fatalf("cursor should already be visible after reaching the last item via keyboard nav")
	}
	before := mm.viewport.YOffset

	model, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	mm = model.(Model)

	if mm.viewport.YOffset != before+1 {
		t.Fatalf("expected free-scroll by exactly one line past the last item, got YOffset %d -> %d", before, mm.viewport.YOffset)
	}
}
