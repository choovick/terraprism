package foldtree

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// LogPane is an append-only, auto-scrolling text pane for showing a
// running (or already-finished) process's output — e.g. `terraform plan`
// or `terraform apply` output. It stays pinned to the bottom as new
// content arrives unless the user has manually scrolled up to inspect
// history, exactly like a terminal's own scrollback/tail behavior.
//
// LogPane is deliberately not built on Node/State: log lines are a flat,
// ever-growing list, not a collapsible tree, so it wraps bubbles/viewport
// directly instead of forcing an unrelated shape through the fold-tree
// machinery.
type LogPane struct {
	viewport viewport.Model
	lines    []string
	visible  bool
	ready    bool
}

var _ tea.Model = LogPane{}

// NewLogPane returns an empty, hidden LogPane. Call SetSize before it's
// shown for the first time.
func NewLogPane() *LogPane {
	return &LogPane{}
}

// SetSize resizes the underlying viewport, preserving whether it was
// pinned to the bottom.
func (p *LogPane) SetSize(width, height int) {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	wasAtBottom := !p.ready || p.viewport.AtBottom()
	p.viewport.Width = width
	p.viewport.Height = height
	p.ready = true
	if wasAtBottom {
		p.viewport.GotoBottom()
	}
}

// SetLines bulk-loads a completed document (e.g. `terraform plan`'s
// already-captured output) and resets scroll to the top, since this is
// a document to read, not a stream to follow.
func (p *LogPane) SetLines(lines []string) {
	p.lines = append([]string(nil), lines...)
	p.viewport.SetContent(strings.Join(p.lines, "\n"))
	p.viewport.GotoTop()
}

// Append adds one streamed line. If the pane was already scrolled to the
// bottom, it stays pinned there; if the user had scrolled up to inspect
// earlier output, their position is preserved.
func (p *LogPane) Append(line string) {
	wasAtBottom := p.viewport.AtBottom()
	p.lines = append(p.lines, line)
	p.viewport.SetContent(strings.Join(p.lines, "\n"))
	if wasAtBottom {
		p.viewport.GotoBottom()
	}
}

// Reset clears all content and scroll position — call before a new run's
// output starts arriving.
func (p *LogPane) Reset() {
	p.lines = nil
	p.viewport.SetContent("")
	p.viewport.GotoTop()
}

// SetVisible shows or hides the pane. View returns "" while hidden.
func (p *LogPane) SetVisible(v bool) { p.visible = v }

// Visible reports whether the pane is currently shown.
func (p *LogPane) Visible() bool { return p.visible }

// Toggle flips visibility.
func (p *LogPane) Toggle() { p.visible = !p.visible }

// Init implements tea.Model.
func (p LogPane) Init() tea.Cmd { return nil }

// Update implements tea.Model, handling resize and the pane's own scroll
// keys/mouse wheel. Callers typically only forward key/mouse messages
// here while the pane has focus (see SplitView), so an unfocused pane
// keeps auto-following without a user's stray keypresses scrolling it --
// but tea.WindowSizeMsg is always handled regardless of focus, exactly
// like TreeView, since bubbles/viewport.Update never resizes itself (its
// Width/Height are plain fields the host must set).
func (p LogPane) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if sizeMsg, ok := msg.(tea.WindowSizeMsg); ok {
		p.SetSize(sizeMsg.Width, sizeMsg.Height)
		return p, nil
	}
	var cmd tea.Cmd
	p.viewport, cmd = p.viewport.Update(msg)
	return p, cmd
}

// View renders the pane, or "" when hidden so a host's layout never
// reserves space for it.
func (p LogPane) View() string {
	if !p.visible {
		return ""
	}
	return p.viewport.View()
}
