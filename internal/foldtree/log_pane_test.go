package foldtree

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestLogPaneHiddenByDefaultAndToggle(t *testing.T) {
	p := NewLogPane()
	if p.Visible() {
		t.Fatalf("new LogPane should start hidden")
	}
	p.SetSize(40, 5)
	p.SetLines([]string{"a", "b"})
	if p.View() != "" {
		t.Fatalf("hidden pane's View() must be empty, got %q", p.View())
	}
	p.Toggle()
	if !p.Visible() {
		t.Fatalf("Toggle should show a hidden pane")
	}
	if p.View() == "" {
		t.Fatalf("visible pane with content should render something")
	}
	p.SetVisible(false)
	if p.Visible() {
		t.Fatalf("SetVisible(false) should hide")
	}
}

func TestLogPaneSetLinesResetsToTop(t *testing.T) {
	p := NewLogPane()
	p.SetSize(40, 3)
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	p.SetLines(lines)
	if !p.viewport.AtTop() {
		t.Fatalf("SetLines should reset scroll to top for a document, not follow to bottom")
	}
}

func TestLogPaneAppendFollowsWhenAtBottom(t *testing.T) {
	p := NewLogPane()
	p.SetSize(40, 3)
	for i := 0; i < 10; i++ {
		p.Append(fmt.Sprintf("line %d", i))
	}
	if !p.viewport.AtBottom() {
		t.Fatalf("Append should keep following (stay at bottom) when nothing has scrolled it away")
	}
	view := p.viewport.View()
	if !strings.Contains(view, "line 9") {
		t.Fatalf("expected the latest line visible while following, got:\n%s", view)
	}
}

func TestLogPaneAppendPreservesManualScrollPosition(t *testing.T) {
	p := NewLogPane()
	p.SetSize(40, 3)
	for i := 0; i < 20; i++ {
		p.Append(fmt.Sprintf("line %d", i))
	}
	p.viewport.GotoTop() // user scrolls up to inspect history

	p.Append("line 20")

	if p.viewport.AtBottom() {
		t.Fatalf("appending while the user has scrolled away from the bottom must not yank them back to it")
	}
}

func TestLogPaneResetClearsContentAndScroll(t *testing.T) {
	p := NewLogPane()
	p.SetSize(40, 3)
	for i := 0; i < 10; i++ {
		p.Append(fmt.Sprintf("line %d", i))
	}
	p.Reset()
	if len(p.lines) != 0 {
		t.Fatalf("Reset should clear stored lines, got %d", len(p.lines))
	}
	if !p.viewport.AtTop() {
		t.Fatalf("Reset should leave scroll at top")
	}
}

func TestLogPaneResizePreservesFollowState(t *testing.T) {
	p := NewLogPane()
	p.SetSize(40, 3)
	for i := 0; i < 20; i++ {
		p.Append(fmt.Sprintf("line %d", i))
	}
	if !p.viewport.AtBottom() {
		t.Fatalf("expected to be following before resize")
	}

	p.SetSize(60, 5)
	if !p.viewport.AtBottom() {
		t.Fatalf("resizing while following should stay pinned to the new bottom")
	}

	p.viewport.GotoTop()
	p.SetSize(80, 6)
	if p.viewport.AtBottom() {
		t.Fatalf("resizing while scrolled away should not snap back to the bottom")
	}
}

// Regression: LogPane.Update must handle tea.WindowSizeMsg itself by
// resizing the viewport (bubbles/viewport.Update never resizes on its
// own -- Width/Height are plain fields the host must set), the same way
// TreeView does. A host driving LogPane purely through the tea.Model
// interface (as SplitView/tui.Model do via a raw tea.WindowSizeMsg, not
// a direct SetSize call) would otherwise see a pane stuck at its
// zero-value size forever, rendering as empty content.
func TestLogPaneUpdateHandlesWindowSizeMsg(t *testing.T) {
	p := NewLogPane()
	p.SetVisible(true)
	m, _ := p.Update(tea.WindowSizeMsg{Width: 50, Height: 6})
	updated := m.(LogPane)
	updated.SetLines([]string{"hello"})
	if updated.View() == "" {
		t.Fatalf("expected content to render after resizing via Update(tea.WindowSizeMsg), got empty view")
	}
	if updated.viewport.Width != 50 || updated.viewport.Height != 6 {
		t.Fatalf("expected viewport resized to 50x6, got %dx%d", updated.viewport.Width, updated.viewport.Height)
	}
}

func TestLogPaneUpdateForwardsToViewport(t *testing.T) {
	p := NewLogPane()
	p.SetSize(40, 3)
	for i := 0; i < 20; i++ {
		p.Append(fmt.Sprintf("line %d", i))
	}
	updatedModel, _ := p.Update(nil)
	updated := updatedModel.(LogPane)
	// A no-op message should not crash and should return a usable pane.
	if updated.viewport.Width != 40 {
		t.Fatalf("Update should preserve the underlying viewport state")
	}
}
