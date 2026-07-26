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

func newTestLogPane(lines int) *LogPane {
	p := NewLogPane()
	p.SetSize(40, 3)
	content := make([]string, lines)
	for i := range content {
		content[i] = fmt.Sprintf("line %d", i)
	}
	p.SetLines(content)
	return p
}

func sendLogPaneKey(p *LogPane, s string) {
	m, _ := p.Update(keyMsgFor(s))
	*p = m.(LogPane)
}

func TestLogPaneGGAndShiftGJumpToTopAndBottom(t *testing.T) {
	p := newTestLogPane(50)
	sendLogPaneKey(p, "G")
	if !p.viewport.AtBottom() {
		t.Fatalf("expected 'G' to jump to the bottom")
	}
	sendLogPaneKey(p, "g")
	sendLogPaneKey(p, "g")
	if !p.viewport.AtTop() {
		t.Fatalf("expected 'gg' to jump to the top")
	}
}

func TestLogPaneJKForwardToViewport(t *testing.T) {
	p := newTestLogPane(50)
	before := p.viewport.YOffset
	sendLogPaneKey(p, "j")
	if p.viewport.YOffset <= before {
		t.Fatalf("expected 'j' to scroll down, offset stayed at %d", p.viewport.YOffset)
	}
}

// lineVisible reports whether line index i falls within the viewport's
// current [YOffset, YOffset+Height) window -- the right check for "did
// jumping to a match bring it into view," since SetYOffset clamps to
// maxYOffset for matches near the end of a short document (a match can
// be visible without YOffset exactly equaling its line index).
func lineVisible(p *LogPane, i int) bool {
	return i >= p.viewport.YOffset && i < p.viewport.YOffset+p.viewport.Height
}

func TestLogPaneSearchFindsAndCyclesMatches(t *testing.T) {
	p := NewLogPane()
	p.SetSize(40, 3)
	p.SetLines([]string{"alpha", "nothing here", "beta match", "more filler", "another match line", "zzz"})

	sendLogPaneKey(p, "/")
	if !p.SearchActive() {
		t.Fatalf("expected search to be active after '/'")
	}
	for _, r := range "match" {
		sendLogPaneKey(p, string(r))
	}
	if len(p.matches) != 2 {
		t.Fatalf("expected 2 matches for 'match', got %d: %v", len(p.matches), p.matches)
	}
	if !lineVisible(p, p.matches[0]) {
		t.Fatalf("expected typing to scroll the first match (line %d) into view, got YOffset=%d", p.matches[0], p.viewport.YOffset)
	}

	sendLogPaneKey(p, "enter")
	if p.SearchActive() {
		t.Fatalf("expected search input to close after enter")
	}

	sendLogPaneKey(p, "n")
	if p.matchPos != 1 || !lineVisible(p, p.matches[1]) {
		t.Fatalf("expected 'n' to move to and scroll the next match (line %d) into view, got matchPos=%d YOffset=%d", p.matches[1], p.matchPos, p.viewport.YOffset)
	}
	sendLogPaneKey(p, "n") // wraps back to the first match
	if p.matchPos != 0 || !lineVisible(p, p.matches[0]) {
		t.Fatalf("expected 'n' to wrap to the first match (line %d), got matchPos=%d YOffset=%d", p.matches[0], p.matchPos, p.viewport.YOffset)
	}
}

func TestLogPaneSearchEscClearsQueryAndMatches(t *testing.T) {
	p := newTestLogPane(20)
	sendLogPaneKey(p, "/")
	sendLogPaneKey(p, "l")
	sendLogPaneKey(p, "i")
	sendLogPaneKey(p, "n")
	sendLogPaneKey(p, "e")
	if len(p.matches) == 0 {
		t.Fatalf("setup: expected matches for 'line'")
	}
	sendLogPaneKey(p, "esc")
	if p.SearchActive() {
		t.Fatalf("expected esc to close the search input")
	}
	if p.searchQuery != "" || len(p.matches) != 0 {
		t.Fatalf("expected esc to clear the query and matches, got query=%q matches=%v", p.searchQuery, p.matches)
	}
}

func TestLogPaneViewSearchBarStates(t *testing.T) {
	p := newTestLogPane(10)
	if bar := p.ViewSearchBar(nil); bar != "" {
		t.Fatalf("expected empty search bar when not searching, got %q", bar)
	}
	sendLogPaneKey(p, "/")
	if bar := p.ViewSearchBar(nil); !strings.Contains(bar, "Search output:") {
		t.Fatalf("expected a search prompt while typing, got %q", bar)
	}
	for _, r := range "line 3" {
		sendLogPaneKey(p, string(r))
	}
	sendLogPaneKey(p, "enter")
	bar := p.ViewSearchBar(nil)
	if !strings.Contains(bar, "line 3") || !strings.Contains(bar, "1/1") {
		t.Fatalf("expected an applied-query status line with a 1/1 match count, got %q", bar)
	}
}
