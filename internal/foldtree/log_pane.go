package foldtree

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
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
// machinery. It does supply its own g/G (top/bottom) and "/"-search,
// mirroring TreeView's own key vocabulary — bubbles/viewport's own
// default keymap already covers j/k/u/d/pgup/pgdown.
type LogPane struct {
	viewport viewport.Model
	lines    []string
	visible  bool
	ready    bool
	pendingG bool

	searching   bool
	searchInput textinput.Model
	searchQuery string
	matches     []int // line indices (into lines) matching searchQuery
	matchPos    int   // index into matches of the current one
}

var _ tea.Model = LogPane{}

// NewLogPane returns an empty, hidden LogPane. Call SetSize before it's
// shown for the first time.
func NewLogPane() *LogPane {
	ti := textinput.New()
	ti.Placeholder = "Search..."
	ti.CharLimit = 200
	ti.Width = 40
	return &LogPane{searchInput: ti}
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
// a document to read, not a stream to follow. Clears any active search,
// since it was matching a now-replaced document.
func (p *LogPane) SetLines(lines []string) {
	p.lines = append([]string(nil), lines...)
	p.viewport.SetContent(strings.Join(p.lines, "\n"))
	p.viewport.GotoTop()
	p.clearSearch()
}

// Append adds one streamed line. If the pane was already scrolled to the
// bottom, it stays pinned there; if the user had scrolled up to inspect
// earlier output, their position is preserved. An active search's
// matches are not recomputed against the new line until the user
// re-searches -- search reflects a point in time, not a live filter.
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
	p.clearSearch()
}

// SetVisible shows or hides the pane. View returns "" while hidden.
func (p *LogPane) SetVisible(v bool) { p.visible = v }

// Visible reports whether the pane is currently shown.
func (p *LogPane) Visible() bool { return p.visible }

// Toggle flips visibility.
func (p *LogPane) Toggle() { p.visible = !p.visible }

// Width reports the pane's current content width.
func (p LogPane) Width() int { return p.viewport.Width }

// Height reports the pane's current content height.
func (p LogPane) Height() int { return p.viewport.Height }

// AtTop reports whether the pane is scrolled to the very top.
func (p LogPane) AtTop() bool { return p.viewport.AtTop() }

// SearchActive reports whether the search input currently has focus --
// a host driving both a LogPane and something else off the same
// keystrokes (e.g. tui.Model's TreeView) should forward every key here
// unconditionally while this is true, the same way TreeView.SearchActive
// gates its own host.
func (p LogPane) SearchActive() bool { return p.searching }

// Init implements tea.Model.
func (p LogPane) Init() tea.Cmd { return nil }

// Update implements tea.Model. tea.WindowSizeMsg is always handled
// (bubbles/viewport.Update never resizes itself — Width/Height are plain
// fields the host must set); tea.KeyMsg is handled by handleKey (g/G,
// "/" search, n/N, else forwarded to the viewport's own default keymap
// for j/k/u/d/pgup/pgdown); anything else (e.g. mouse wheel) forwards
// straight to the viewport.
func (p LogPane) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.SetSize(msg.Width, msg.Height)
		return p, nil
	case tea.KeyMsg:
		return p.handleKey(msg)
	}
	var cmd tea.Cmd
	p.viewport, cmd = p.viewport.Update(msg)
	return p, cmd
}

func (p LogPane) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if p.searching {
		return p.handleSearchKey(msg)
	}

	switch msg.String() {
	case "/":
		p.searching = true
		p.searchInput.Focus()
		return p, textinput.Blink
	case "n":
		p.jumpToMatch(p.matchPos + 1)
		return p, nil
	case "N":
		p.jumpToMatch(p.matchPos - 1)
		return p, nil
	case "g":
		if p.pendingG {
			p.viewport.GotoTop()
			p.pendingG = false
		} else {
			p.pendingG = true
		}
		return p, nil
	case "G":
		p.viewport.GotoBottom()
		p.pendingG = false
		return p, nil
	}

	p.pendingG = false
	var cmd tea.Cmd
	p.viewport, cmd = p.viewport.Update(msg)
	return p, cmd
}

func (p LogPane) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		p.searching = false
		return p, nil
	case "esc":
		p.clearSearch()
		return p, nil
	default:
		var cmd tea.Cmd
		p.searchInput, cmd = p.searchInput.Update(msg)
		p.searchQuery = p.searchInput.Value()
		p.recomputeMatches()
		if len(p.matches) > 0 {
			p.jumpToMatch(0)
		}
		return p, cmd
	}
}

// recomputeMatches finds every line containing searchQuery
// (case-insensitive substring, not fuzzy — log text is grepped, not
// fuzzy-matched the way a short resource address is).
func (p *LogPane) recomputeMatches() {
	p.matches = p.matches[:0]
	p.matchPos = 0
	if p.searchQuery == "" {
		return
	}
	q := strings.ToLower(p.searchQuery)
	for i, line := range p.lines {
		if strings.Contains(strings.ToLower(line), q) {
			p.matches = append(p.matches, i)
		}
	}
}

// jumpToMatch moves to match index pos (wrapping) and scrolls that line
// into view at the top of the viewport.
func (p *LogPane) jumpToMatch(pos int) {
	if len(p.matches) == 0 {
		return
	}
	pos = ((pos % len(p.matches)) + len(p.matches)) % len(p.matches)
	p.matchPos = pos
	p.viewport.SetYOffset(p.matches[pos])
}

func (p *LogPane) clearSearch() {
	p.searching = false
	p.searchQuery = ""
	p.searchInput.SetValue("")
	p.matches = nil
	p.matchPos = 0
}

// ViewSearchBar renders the search input line (while typing) or the
// "query (n/m matches)" status line (once a query is applied), or ""
// when no search is active. style wraps the rendered text; pass nil for
// no styling.
func (p LogPane) ViewSearchBar(style func(string) string) string {
	if style == nil {
		style = func(s string) string { return s }
	}
	if p.searching {
		return style("Search output: ") + p.searchInput.View() + "\n"
	}
	if p.searchQuery != "" {
		return style(fmt.Sprintf("Search output: %q (%d/%d matches)", p.searchQuery, p.matchPos+1, len(p.matches))) + "\n"
	}
	return ""
}

// View renders the pane, or "" when hidden so a host's layout never
// reserves space for it.
func (p LogPane) View() string {
	if !p.visible {
		return ""
	}
	return p.viewport.View()
}
