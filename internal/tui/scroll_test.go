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

// Regression: repeatedly free-scrolling past the last item used to enter
// an unstable two-value oscillation instead of settling. Once the
// viewport's top line advances to exactly match the selected line,
// cursorLineVisible reports the line as still visible (it's the top edge
// of the visible range), so the old "free-scroll by one more line while
// visible" branch scrolled one line past it — which made the line
// invisible on the very next press, triggering a snap-back to the exact
// offset it had just left, then immediately free-scrolling past it
// again, forever. It must instead settle with the selected line pinned
// at the top of the viewport and stop.
func TestFreeScrollPastLastItemSettlesWithoutOscillating(t *testing.T) {
	plan := manyResourcesPlan(40)
	m := NewModel(plan, "")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	mm := model.(Model)

	for i := 0; i < len(plan.Resources)-1; i++ {
		model, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		mm = model.(Model)
	}

	var offsets []int
	for i := 0; i < 60; i++ {
		model, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		mm = model.(Model)
		offsets = append(offsets, mm.viewport.YOffset)
		if !mm.cursorLineVisible() {
			t.Fatalf("press #%d: selection scrolled out of view (YOffset=%d, selectedLineStart=%d)",
				i+1, mm.viewport.YOffset, mm.selectedLineStart)
		}
	}

	last := offsets[len(offsets)-1]
	for i := len(offsets) - 10; i < len(offsets); i++ {
		if offsets[i] != last {
			t.Fatalf("viewport did not settle, still changing after 50+ presses: %v", offsets[len(offsets)-15:])
		}
	}
	if mm.selectedLineStart != last {
		t.Fatalf("expected the selected line to settle pinned at the top of the viewport (YOffset=%d), got selectedLineStart=%d",
			last, mm.selectedLineStart)
	}
}

// Regression: reaching the last fold block of an expanded resource that
// is itself the last (or only) displayed resource used to keep
// free-scrolling the viewport by one line per keypress even though the
// selected block had stopped changing — because the "free-scroll past
// the boundary" branch didn't distinguish "stuck at the plain resource
// row" (where free-scroll reveals the End-of-Plan footer, a real
// feature) from "stuck deep inside a fold hierarchy with nothing further
// to select" (where further scrolling only drifts the view away from a
// selection that isn't moving). Once truly stuck at the last fold block,
// further 'j' presses must be a no-op.
func TestLastFoldBlockOfLastResourceStopsScrollingOnceStuck(t *testing.T) {
	nested := mapBlock("values", tfplan.ActionUpdate,
		leaf("replicaCount", tfplan.ActionUpdate, tfplan.KindNumber, "2", "3"))
	metadata := mapBlock("metadata", tfplan.ActionUpdate, nested)

	resources := []tfplan.Resource{
		{Address: "helm_release.only", Action: tfplan.ActionUpdate, Attributes: withPaths([]tfplan.Attribute{metadata}, "")},
	}
	plan := &tfplan.Plan{Resources: resources}

	m := NewModel(plan, "")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	mm := model.(Model)

	model, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter}) // expand the resource
	mm = model.(Model)
	model, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")}) // expand its sub-blocks
	mm = model.(Model)

	blocks := mm.currentFoldBlocks()
	if len(blocks) == 0 {
		t.Fatalf("expected at least one fold block")
	}

	// Step down to the last fold block.
	for i := 0; i < len(blocks); i++ {
		model, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		mm = model.(Model)
	}
	if mm.blockCursor != len(blocks)-1 {
		t.Fatalf("expected blockCursor at the last block (%d), got %d", len(blocks)-1, mm.blockCursor)
	}
	stuckOffset := mm.viewport.YOffset
	stuckSelectedLine := mm.selectedLineStart

	// Further 'j' presses must not move the viewport or the selection —
	// there's nothing left to select.
	for i := 0; i < 5; i++ {
		model, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		mm = model.(Model)
		if mm.blockCursor != len(blocks)-1 {
			t.Fatalf("blockCursor should stay at the last block, got %d", mm.blockCursor)
		}
		if mm.viewport.YOffset != stuckOffset {
			t.Fatalf("viewport should stop scrolling once stuck at the last block, got YOffset %d -> %d", stuckOffset, mm.viewport.YOffset)
		}
		if mm.selectedLineStart != stuckSelectedLine {
			t.Fatalf("selectedLineStart should stay put once stuck, got %d -> %d", stuckSelectedLine, mm.selectedLineStart)
		}
	}
}
