package tui

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wordwrap"

	"github.com/CaptShanks/terraprism/internal/tfplan"
	"github.com/CaptShanks/terraprism/internal/updater"
)

// Model represents the TUI state
type Model struct {
	plan               *tfplan.Plan
	cursor             int
	expanded           map[int]bool
	foldedBlocks       map[string]bool
	blockCursor        int
	diffContext        int
	viewport           viewport.Model
	ready              bool
	width              int
	height             int
	searching          bool
	searchInput        textinput.Model
	searchQuery        string
	searchMatches      []int
	currentMatch       int
	pendingG           bool  // Track if 'g' was pressed, waiting for second 'g'
	resourceLineStarts []int // rendered line offset per resource (populated during render)
	selectedLineStart  int   // rendered line offset for the current resource or sub-block cursor
	contentLineCount   int   // total rendered content lines (excluding padding)

	// Apply mode fields
	applyMode    bool   // Whether apply is available
	planFile     string // Path to the plan file
	tfCommand    string // "terraform" or "tofu"
	shouldApply  bool   // User pressed 'a' to apply
	confirmApply bool   // Waiting for confirmation

	// Status filter fields
	statusFilters map[tfplan.Action]bool // true = show resources with this action
	filtering     bool                   // filter picker is open
	filterCursor  int                    // cursor in filter picker

	// Sort fields
	sortOrder  SortOrder // default, byAction, byAddress, byType
	sorting    bool      // sort picker is open
	sortCursor int       // cursor in sort picker

	// Update nudge
	currentVersion  string // for update check
	updateAvailable string // non-empty when newer version available
}

// UpdateAvailableMsg is sent when an update check finds a newer version.
type UpdateAvailableMsg struct {
	Version string
}

// SortOrder defines how resources are ordered
type SortOrder string

const (
	SortDefault   SortOrder = "default"
	SortByAction  SortOrder = "action"
	SortByAddress SortOrder = "address"
	SortByType    SortOrder = "type"
)

// sortOptions is the ordered list of sort choices for the picker
var sortOptions = []SortOrder{SortDefault, SortByAction, SortByAddress, SortByType}

// actionOrder defines sort order for actions (destructive last)
var actionOrder = map[tfplan.Action]int{
	tfplan.ActionCreate:  0,
	tfplan.ActionRead:    1,
	tfplan.ActionUpdate:  2,
	tfplan.ActionReplace: 3,
	tfplan.ActionForget:  4,
	tfplan.ActionDelete:  5,
	tfplan.ActionOutput:  6,
	tfplan.ActionNoOp:    7,
}

// filterableActions is the ordered list of statuses available for filtering
var filterableActions = []tfplan.Action{
	tfplan.ActionCreate,
	tfplan.ActionDelete,
	tfplan.ActionUpdate,
	tfplan.ActionReplace,
	tfplan.ActionRead,
	tfplan.ActionForget,
	tfplan.ActionOutput,
}

// filteredResources returns indices into plan.Resources that pass the status filter.
// When statusFilters is empty or nil, returns all indices.
func (m *Model) filteredResources() []int {
	if len(m.statusFilters) == 0 {
		indices := make([]int, len(m.plan.Resources))
		for i := range m.plan.Resources {
			indices[i] = i
		}
		return indices
	}
	var indices []int
	for i, r := range m.plan.Resources {
		if m.statusFilters[r.Action] {
			indices = append(indices, i)
		}
	}
	return indices
}

// sortedResources returns filtered indices sorted by the current sort order.
func (m *Model) sortedResources() []int {
	filtered := m.filteredResources()
	if m.sortOrder == SortDefault || m.sortOrder == "" {
		return filtered
	}
	sort.Slice(filtered, func(i, j int) bool {
		ri := m.plan.Resources[filtered[i]]
		rj := m.plan.Resources[filtered[j]]
		switch m.sortOrder {
		case SortByAction:
			oi, oki := actionOrder[ri.Action]
			oj, okj := actionOrder[rj.Action]
			if !oki {
				oi = 99
			}
			if !okj {
				oj = 99
			}
			if oi != oj {
				return oi < oj
			}
			return ri.Address < rj.Address
		case SortByAddress:
			return ri.Address < rj.Address
		case SortByType:
			if ri.Type != rj.Type {
				return ri.Type < rj.Type
			}
			return ri.Address < rj.Address
		}
		return false
	})
	return filtered
}

// displayedResourceIndices returns the resource indices to display.
// When searchQuery is empty: returns sortedResources() (all filtered/sorted).
// When searchQuery is non-empty: returns only matching resources (filtered by search).
func (m *Model) displayedResourceIndices() []int {
	sorted := m.sortedResources()
	if m.searchQuery == "" {
		return sorted
	}
	if len(m.searchMatches) == 0 {
		return []int{} // no matches, show empty
	}
	// searchMatches holds display indices into sorted; map to resource indices
	result := make([]int, 0, len(m.searchMatches))
	for _, displayIdx := range m.searchMatches {
		if displayIdx >= 0 && displayIdx < len(sorted) {
			result = append(result, sorted[displayIdx])
		}
	}
	return result
}

// NewModel creates a new TUI model (view-only mode)
func NewModel(plan *tfplan.Plan, version string) Model {
	ti := textinput.New()
	ti.Placeholder = "Search..."
	ti.CharLimit = 100
	ti.Width = 40

	return Model{
		plan:           withDisplayResources(plan),
		expanded:       make(map[int]bool),
		foldedBlocks:   make(map[string]bool),
		blockCursor:    -1,
		diffContext:    defaultDiffContext,
		searchInput:    ti,
		searchMatches:  []int{},
		applyMode:      false,
		statusFilters:  nil, // nil = show all
		sortOrder:      SortDefault,
		currentVersion: version,
	}
}

// NewModelWithApply creates a TUI model with apply capability
func NewModelWithApply(plan *tfplan.Plan, planFile, tfCommand, version string) Model {
	ti := textinput.New()
	ti.Placeholder = "Search..."
	ti.CharLimit = 100
	ti.Width = 40

	return Model{
		plan:           withDisplayResources(plan),
		expanded:       make(map[int]bool),
		foldedBlocks:   make(map[string]bool),
		blockCursor:    -1,
		diffContext:    defaultDiffContext,
		searchInput:    ti,
		searchMatches:  []int{},
		applyMode:      true,
		planFile:       planFile,
		tfCommand:      tfCommand,
		statusFilters:  nil, // nil = show all
		sortOrder:      SortDefault,
		currentVersion: version,
	}
}

// withDisplayResources returns a Plan whose Resources includes output
// changes as synthetic entries (see tfplan.Plan.DisplayResources), so the
// rest of the model's rendering/navigation/filtering/sorting code — which
// only ever reads plan.Resources — picks them up automatically without
// needing its own separate output-change code path. The original plan is
// left untouched (callers may still hold onto it, e.g. for history).
func withDisplayResources(plan *tfplan.Plan) *tfplan.Plan {
	if plan == nil || len(plan.OutputChanges) == 0 {
		return plan
	}
	display := *plan
	display.Resources = plan.DisplayResources()
	return &display
}

// ShouldApply returns true if user chose to apply
func (m Model) ShouldApply() bool {
	return m.shouldApply
}

// Init initializes the model
func (m Model) Init() tea.Cmd {
	if m.currentVersion == "" || updater.IsSkipUpdateCheck() {
		return nil
	}
	return checkUpdateCmd(m.currentVersion)
}

// checkUpdateCmd runs an async update check and sends UpdateAvailableMsg if an update is available.
func checkUpdateCmd(version string) tea.Cmd {
	return func() tea.Msg {
		latest, hasUpdate, err := updater.CheckLatestWithCache(version, updater.UpdateCheckIntervalDays())
		if err != nil || !hasUpdate {
			return nil
		}
		return UpdateAvailableMsg{Version: latest}
	}
}

// Update handles messages
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case UpdateAvailableMsg:
		m.updateAvailable = msg.Version
		// Resize viewport to account for the extra footer line
		if m.ready && m.height > 0 {
			headerHeight := 4
			footerHeight := 4 // help + nudge
			m.viewport.Height = m.height - headerHeight - footerHeight
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		headerHeight := 4 // Title + summary + blank line
		footerHeight := 3 // Help text
		if m.updateAvailable != "" {
			footerHeight = 4 // +1 for update nudge line
		}

		if !m.ready {
			m.viewport = viewport.New(msg.Width-4, msg.Height-headerHeight-footerHeight)
			m.viewport.YPosition = headerHeight
			m.ready = true
		} else {
			m.viewport.Width = msg.Width - 4
			m.viewport.Height = msg.Height - headerHeight - footerHeight
		}
		m.updateViewportContent()

	case tea.KeyMsg:
		if m.filtering {
			return m.handleFilterKey(msg)
		}
		if m.sorting {
			return m.handleSortKey(msg)
		}
		if m.searching {
			switch msg.String() {
			case "enter":
				m.searching = false
				m.searchQuery = m.searchInput.Value()
				m.performSearch()
				m.clampCursorAndRefreshSearch()
				m.updateViewportContent()
			case "esc":
				m.searching = false
				m.searchInput.SetValue("")
				m.searchQuery = ""
				m.searchMatches = []int{}
				m.clampCursorAndRefreshSearch()
				m.updateViewportContent()
			case "up":
				return m.handleSearchArrowUp(), nil
			case "down":
				return m.handleSearchArrowDown(), nil
			default:
				m.searchInput, cmd = m.searchInput.Update(msg)
				m.searchQuery = m.searchInput.Value()
				m.performSearch()
				m.clampCursorAndRefreshSearch()
				m.updateViewportContent()
				cmds = append(cmds, cmd)
			}
		} else {
			return m.handleNormalKey(msg)
		}

	case tea.MouseMsg:
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

const (
	defaultDiffContext = 3
	diffContextStep    = 3
	maxDiffContext     = 30
)

func (m Model) diffContextSize() int {
	return clampDiffContext(m.diffContext)
}

func clampDiffContext(context int) int {
	if context < 0 {
		return 0
	}
	if context > maxDiffContext {
		return maxDiffContext
	}
	return context
}

// normalKeyHandler handles a single key in normal mode. Returns (model, cmd, quit).
type normalKeyHandler func(m Model) (Model, tea.Cmd, bool)

var normalKeyHandlers = map[string]normalKeyHandler{
	"q":         func(m Model) (Model, tea.Cmd, bool) { return m, tea.Quit, true },
	"ctrl+c":    func(m Model) (Model, tea.Cmd, bool) { return m, tea.Quit, true },
	"up":        handleKeyUp,
	"k":         handleKeyUp,
	"down":      handleKeyDown,
	"j":         handleKeyDown,
	"enter":     handleKeyEnter,
	" ":         handleKeyEnter,
	"e":         handleKeyExpandAll,
	"E":         handleKeyExpandEverything,
	"c":         handleKeyCollapseAll,
	"C":         handleKeyCollapseEverything,
	"f":         handleKeyFilter,
	"s":         handleKeySort,
	"/":         handleKeySearch,
	"n":         handleKeyNextMatch,
	"N":         handleKeyPrevMatch,
	"esc":       handleKeyEsc,
	"backspace": handleKeyCollapseCurrent,
	"h":         handleKeyCollapseCurrent,
	"left":      handleKeyCollapseCurrent,
	"d":         handleKeyHalfPageDown,
	"ctrl+d":    handleKeyHalfPageDown,
	"u":         handleKeyHalfPageUp,
	"ctrl+u":    handleKeyHalfPageUp,
	"ctrl+e":    handleKeyScrollLineDown,
	"ctrl+y":    handleKeyScrollLineUp,
	"+":         handleKeyIncreaseDiffContext,
	"=":         handleKeyIncreaseDiffContext,
	"-":         handleKeyDecreaseDiffContext,
	"g":         handleKeyG,
	"G":         handleKeyGG,
	"pgup":      handleKeyPgUp,
	"pgdown":    handleKeyPgDown,
	"l":         handleKeyExpandCurrent,
	"right":     handleKeyExpandCurrent,
	"a":         handleKeyApply,
	"y":         handleKeyConfirmApply,
}

func handleKeyUp(m Model) (Model, tea.Cmd, bool) {
	if m.blockCursor >= 0 {
		m.blockCursor--
		m.updateViewportContent()
		m.ensureCursorVisible()
		return m, nil, true
	}

	if m.cursor > 0 {
		m.cursor--
		if blocks := m.currentFoldBlocks(); m.expanded[m.currentResourceIndex()] && len(blocks) > 0 {
			m.blockCursor = len(blocks) - 1
		}
		m.updateViewportContent()
		m.ensureCursorVisible()
	} else if m.cursorLineVisible() {
		m.viewport.SetYOffset(m.viewport.YOffset - 1)
	} else {
		// Already at the first item, but the mouse wheel scrolled the
		// view away from it — snap back rather than nudging one line at
		// a time toward a cursor that isn't going to move.
		m.ensureCursorVisible()
	}
	return m, nil, true
}

// handleSearchArrowUp handles up arrow in search mode (scroll filtered list)
func (m Model) handleSearchArrowUp() Model {
	if m.cursor > 0 {
		m.cursor--
		m.blockCursor = -1
		m.updateViewportContent()
		m.ensureCursorVisible()
	} else if m.cursorLineVisible() {
		m.viewport.SetYOffset(m.viewport.YOffset - 1)
	} else {
		m.ensureCursorVisible()
	}
	return m
}

// handleSearchArrowDown handles down arrow in search mode (scroll filtered list)
func (m Model) handleSearchArrowDown() Model {
	displayed := m.displayedResourceIndices()
	if m.cursor < len(displayed)-1 {
		m.cursor++
		m.blockCursor = -1
		m.updateViewportContent()
		m.ensureCursorVisible()
	} else if m.cursorLineVisible() {
		m.viewport.SetYOffset(m.viewport.YOffset + 1)
	} else {
		m.ensureCursorVisible()
	}
	return m
}

func handleKeyDown(m Model) (Model, tea.Cmd, bool) {
	if blocks := m.currentFoldBlocks(); m.expanded[m.currentResourceIndex()] && m.blockCursor < len(blocks)-1 {
		m.blockCursor++
		m.updateViewportContent()
		m.ensureCursorVisible()
		return m, nil, true
	}

	filtered := m.displayedResourceIndices()
	if m.cursor < len(filtered)-1 {
		m.cursor++
		m.blockCursor = -1
		m.updateViewportContent()
		m.ensureCursorVisible()
	} else if m.cursorLineVisible() {
		m.viewport.SetYOffset(m.viewport.YOffset + 1)
	} else {
		// Already at the last item, but the mouse wheel scrolled the
		// view away from it — snap back rather than nudging one line at
		// a time toward a cursor that isn't going to move.
		m.ensureCursorVisible()
	}
	return m, nil, true
}

func handleKeyEnter(m Model) (Model, tea.Cmd, bool) {
	if m.toggleCurrentFold() {
		m.updateViewportContent()
		m.ensureCursorVisible()
		return m, nil, true
	}

	filtered := m.displayedResourceIndices()
	if len(filtered) > 0 && m.cursor >= 0 && m.cursor < len(filtered) {
		resourceIdx := filtered[m.cursor]
		m.expanded[resourceIdx] = !m.expanded[resourceIdx]
		m.blockCursor = -1
	}
	m.updateViewportContent()
	m.scrollForExpanded()
	return m, nil, true
}

// handleKeyExpandAll expands the cursor's current scope recursively:
//   - inside a sub-fold: that fold and its descendants
//   - at root: the cursor's resource and all its sub-folds
//
// Use Shift+E (handleKeyExpandEverything) for a global expand across all resources.
func handleKeyExpandAll(m Model) (Model, tea.Cmd, bool) {
	if m.blockCursor >= 0 && m.setCurrentScopeFoldsCollapsed(false) {
		m.updateViewportContent()
		m.ensureCursorVisible()
		return m, nil, true
	}

	filtered := m.displayedResourceIndices()
	if len(filtered) > 0 && m.cursor >= 0 && m.cursor < len(filtered) {
		m.expanded[filtered[m.cursor]] = true
		m.setCurrentScopeFoldsCollapsed(false)
	}
	m.updateViewportContent()
	m.ensureCursorVisible()
	return m, nil, true
}

// handleKeyCollapseAll collapses the cursor's current scope recursively.
// Use Shift+C (handleKeyCollapseEverything) for a global collapse.
func handleKeyCollapseAll(m Model) (Model, tea.Cmd, bool) {
	if m.blockCursor >= 0 && m.setCurrentScopeFoldsCollapsed(true) {
		m.updateViewportContent()
		m.ensureCursorVisible()
		return m, nil, true
	}

	filtered := m.displayedResourceIndices()
	if len(filtered) > 0 && m.cursor >= 0 && m.cursor < len(filtered) {
		idx := filtered[m.cursor]
		m.setCurrentScopeFoldsCollapsed(true)
		m.expanded[idx] = false
		m.blockCursor = -1
	}
	m.updateViewportContent()
	m.ensureCursorVisible()
	return m, nil, true
}

func handleKeyExpandEverything(m Model) (Model, tea.Cmd, bool) {
	m.expandEverything()
	return m, nil, true
}

func handleKeyCollapseEverything(m Model) (Model, tea.Cmd, bool) {
	m.collapseEverything()
	return m, nil, true
}

func handleKeyFilter(m Model) (Model, tea.Cmd, bool) {
	m.filtering = true
	m.filterCursor = 0
	if m.statusFilters == nil {
		m.statusFilters = make(map[tfplan.Action]bool)
	}
	return m, nil, true
}

func handleKeySort(m Model) (Model, tea.Cmd, bool) {
	m.sorting = true
	m.sortCursor = 0
	for i, opt := range sortOptions {
		if opt == m.sortOrder {
			m.sortCursor = i
			break
		}
	}
	return m, nil, true
}

func handleKeySearch(m Model) (Model, tea.Cmd, bool) {
	m.searching = true
	m.searchInput.Focus()
	return m, textinput.Blink, true
}

func handleKeyNextMatch(m Model) (Model, tea.Cmd, bool) {
	m.nextMatch()
	return m, nil, true
}

func handleKeyPrevMatch(m Model) (Model, tea.Cmd, bool) {
	m.prevMatch()
	return m, nil, true
}

func handleKeyEsc(m Model) (Model, tea.Cmd, bool) {
	if len(m.statusFilters) > 0 {
		m.statusFilters = nil
		m.clampCursorAndRefreshSearch()
		m.updateViewportContent()
	} else {
		m.clearSearch()
	}
	return m, nil, true
}

func handleKeyCollapseCurrent(m Model) (Model, tea.Cmd, bool) {
	if m.setCurrentFoldCollapsed(true) {
		m.updateViewportContent()
		m.ensureCursorVisible()
		return m, nil, true
	}

	filtered := m.displayedResourceIndices()
	if len(filtered) > 0 && m.cursor >= 0 && m.cursor < len(filtered) {
		m.expanded[filtered[m.cursor]] = false
		m.blockCursor = -1
	}
	m.updateViewportContent()
	m.ensureCursorVisible()
	return m, nil, true
}

func handleKeyHalfPageDown(m Model) (Model, tea.Cmd, bool) {
	m.scrollHalfPageDown()
	return m, nil, true
}

func handleKeyHalfPageUp(m Model) (Model, tea.Cmd, bool) {
	m.scrollHalfPageUp()
	return m, nil, true
}

func handleKeyScrollLineDown(m Model) (Model, tea.Cmd, bool) {
	m.viewport.SetYOffset(m.viewport.YOffset + 1)
	return m, nil, true
}

func handleKeyScrollLineUp(m Model) (Model, tea.Cmd, bool) {
	newOffset := m.viewport.YOffset - 1
	if newOffset < 0 {
		newOffset = 0
	}
	m.viewport.SetYOffset(newOffset)
	return m, nil, true
}

func handleKeyIncreaseDiffContext(m Model) (Model, tea.Cmd, bool) {
	m.diffContext = clampDiffContext(m.diffContextSize() + diffContextStep)
	m.updateViewportContent()
	m.ensureCursorVisible()
	return m, nil, true
}

func handleKeyDecreaseDiffContext(m Model) (Model, tea.Cmd, bool) {
	m.diffContext = clampDiffContext(m.diffContextSize() - diffContextStep)
	m.updateViewportContent()
	m.ensureCursorVisible()
	return m, nil, true
}

func handleKeyG(m Model) (Model, tea.Cmd, bool) {
	m.handleGKey()
	return m, nil, true
}

func handleKeyGG(m Model) (Model, tea.Cmd, bool) {
	m.gotoBottom()
	return m, nil, true
}

func handleKeyPgUp(m Model) (Model, tea.Cmd, bool) {
	m.viewport.GotoTop()
	m.viewport.SetYOffset(m.viewport.YOffset - m.viewport.Height)
	return m, nil, true
}

func handleKeyPgDown(m Model) (Model, tea.Cmd, bool) {
	m.viewport.SetYOffset(m.viewport.YOffset + m.viewport.Height)
	return m, nil, true
}

func handleKeyExpandCurrent(m Model) (Model, tea.Cmd, bool) {
	if m.setCurrentFoldCollapsed(false) {
		m.updateViewportContent()
		m.ensureCursorVisible()
		return m, nil, true
	}

	filtered := m.displayedResourceIndices()
	if len(filtered) > 0 && m.cursor >= 0 && m.cursor < len(filtered) {
		m.expanded[filtered[m.cursor]] = true
	}
	m.updateViewportContent()
	m.scrollForExpanded()
	return m, nil, true
}

func handleKeyApply(m Model) (Model, tea.Cmd, bool) {
	if m.applyMode {
		if m.confirmApply {
			m.shouldApply = true
			return m, tea.Quit, true
		}
		m.confirmApply = true
		m.updateViewportContent()
	}
	return m, nil, true
}

func handleKeyConfirmApply(m Model) (Model, tea.Cmd, bool) {
	if m.applyMode && m.confirmApply {
		m.shouldApply = true
		return m, tea.Quit, true
	}
	return m, nil, true
}

// handleNormalKey handles key presses in normal (non-search) mode
func (m Model) handleNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key != "g" && key != "G" {
		m.pendingG = false
	}

	if handler, ok := normalKeyHandlers[key]; ok {
		newM, cmd, _ := handler(m)
		if m.confirmApply && key != "a" && key != "y" {
			newM.confirmApply = false
			newM.updateViewportContent()
		}
		return newM, cmd
	}

	if m.confirmApply {
		m.confirmApply = false
		m.updateViewportContent()
	}
	return m, nil
}

// handleFilterKey handles key presses in filter picker mode
func (m Model) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.statusFilters = nil
		m.filtering = false
		m.clampCursorAndRefreshSearch()
		m.updateViewportContent()
		return m, nil

	case "enter":
		// Toggle on Space, apply and close on Enter (when not toggling)
		// Enter toggles too per plan - "Space/Enter: toggle selected status on/off"
		// So Enter both toggles and... the plan says "Enter: Apply and close". Let me re-read.
		// "Space/Enter: toggle selected status on/off" and "Enter: Apply and close"
		// So Enter toggles the current selection AND applies/closes? Or Enter just applies?
		// Typical UX: Space toggles, Enter applies and closes. So we need to not toggle on Enter, just close.
		// Actually "Enter (when not toggling): apply filters and close" - so Enter = apply and close, don't toggle.
		m.filtering = false
		m.clampCursorAndRefreshSearch()
		m.updateViewportContent()
		return m, nil

	case "up", "k":
		if m.filterCursor > 0 {
			m.filterCursor--
		}
		return m, nil

	case "down", "j":
		if m.filterCursor < len(filterableActions)-1 {
			m.filterCursor++
		}
		return m, nil

	case " ":
		// Space toggles the selected status
		action := filterableActions[m.filterCursor]
		m.statusFilters[action] = !m.statusFilters[action]
		return m, nil

	case "a":
		// Select all
		for _, action := range filterableActions {
			m.statusFilters[action] = true
		}
		return m, nil

	case "c":
		// Clear all filters (show all)
		m.statusFilters = make(map[tfplan.Action]bool)
		return m, nil
	}

	return m, nil
}

// handleSortKey handles key presses in sort picker mode
func (m Model) handleSortKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.sorting = false
		m.updateViewportContent()
		return m, nil

	case "enter", " ":
		m.sortOrder = sortOptions[m.sortCursor]
		m.sorting = false
		m.clampCursorAndRefreshSearch()
		m.updateViewportContent()
		return m, nil

	case "up", "k":
		if m.sortCursor > 0 {
			m.sortCursor--
		}
		return m, nil

	case "down", "j":
		if m.sortCursor < len(sortOptions)-1 {
			m.sortCursor++
		}
		return m, nil
	}

	return m, nil
}

// clampCursorAndRefreshSearch clamps cursor to valid range after filter/sort change and re-runs search
func (m *Model) clampCursorAndRefreshSearch() {
	displayed := m.displayedResourceIndices()
	if m.cursor >= len(displayed) {
		if len(displayed) > 0 {
			m.cursor = len(displayed) - 1
		} else {
			m.cursor = 0
		}
	}
	m.blockCursor = -1
	if m.searchQuery != "" {
		m.performSearch()
	}
}

func (m Model) currentResourceIndex() int {
	displayed := m.displayedResourceIndices()
	if len(displayed) == 0 || m.cursor < 0 || m.cursor >= len(displayed) {
		return -1
	}
	return displayed[m.cursor]
}

// currentFoldBlocks returns the fold blocks currently visible (i.e. not
// hidden inside a collapsed ancestor) for the cursor's resource, in
// display order — used for blockCursor navigation.
func (m Model) currentFoldBlocks() []foldBlock {
	resourceIdx := m.currentResourceIndex()
	if resourceIdx < 0 || resourceIdx >= len(m.plan.Resources) {
		return nil
	}
	r := m.plan.Resources[resourceIdx]
	return flattenFoldableAttributes(r.Address, r.Attributes, 0, m.resolveCollapsed)
}

func (m *Model) currentFoldBlock() (foldBlock, bool) {
	blocks := m.currentFoldBlocks()
	if m.blockCursor < 0 || m.blockCursor >= len(blocks) {
		return foldBlock{}, false
	}
	return blocks[m.blockCursor], true
}

func (m *Model) toggleCurrentFold() bool {
	block, ok := m.currentFoldBlock()
	if !ok {
		return false
	}
	m.foldedBlocks[block.Key] = !m.isFoldCollapsed(block)
	return true
}

func (m *Model) setCurrentFoldCollapsed(collapsed bool) bool {
	block, ok := m.currentFoldBlock()
	if !ok {
		return false
	}
	m.foldedBlocks[block.Key] = collapsed
	return true
}

// setCurrentScopeFoldsCollapsed sets the collapsed state of the cursor's
// current fold block and all its descendants (structurally, regardless of
// their current visibility), or of the whole resource's fold tree when no
// sub-fold is selected.
func (m *Model) setCurrentScopeFoldsCollapsed(collapsed bool) bool {
	resourceIdx := m.currentResourceIndex()
	if resourceIdx < 0 || resourceIdx >= len(m.plan.Resources) || !m.expanded[resourceIdx] {
		return false
	}

	r := m.plan.Resources[resourceIdx]
	blocks := allFoldableAttributes(r.Address, r.Attributes, 0)
	if len(blocks) == 0 {
		return false
	}

	if current, ok := m.currentFoldBlock(); ok {
		changed := false
		for _, block := range blocks {
			if block.Path == current.Path || isDescendantPath(block.Path, current.Path) {
				m.foldedBlocks[block.Key] = collapsed
				changed = true
			}
		}
		return changed
	}

	for _, block := range blocks {
		m.foldedBlocks[block.Key] = collapsed
	}
	return true
}

func (m *Model) setDisplayedFoldsCollapsed(collapsed bool) {
	for _, resourceIdx := range m.displayedResourceIndices() {
		if resourceIdx < 0 || resourceIdx >= len(m.plan.Resources) {
			continue
		}
		r := m.plan.Resources[resourceIdx]
		for _, block := range allFoldableAttributes(r.Address, r.Attributes, 0) {
			m.foldedBlocks[block.Key] = collapsed
		}
	}
}

// expandEverything expands all visible resources and their nested fold blocks.
func (m *Model) expandEverything() {
	for _, idx := range m.displayedResourceIndices() {
		m.expanded[idx] = true
	}
	m.setDisplayedFoldsCollapsed(false)
	m.blockCursor = -1
	m.updateViewportContent()
	m.ensureCursorVisible()
}

// collapseEverything collapses all visible resources and their nested fold blocks.
func (m *Model) collapseEverything() {
	for _, idx := range m.displayedResourceIndices() {
		m.expanded[idx] = false
	}
	m.setDisplayedFoldsCollapsed(true)
	m.blockCursor = -1
	m.updateViewportContent()
	m.ensureCursorVisible()
}

// nextMatch moves to the next search match
func (m *Model) nextMatch() {
	if m.searchQuery == "" || len(m.searchMatches) == 0 {
		return
	}
	displayed := m.displayedResourceIndices()
	if len(displayed) > 0 {
		m.currentMatch = (m.currentMatch + 1) % len(displayed)
		m.cursor = m.currentMatch
		m.updateViewportContent()
		m.ensureCursorVisible()
	}
}

// prevMatch moves to the previous search match
func (m *Model) prevMatch() {
	if m.searchQuery == "" || len(m.searchMatches) == 0 {
		return
	}
	displayed := m.displayedResourceIndices()
	if len(displayed) > 0 {
		m.currentMatch--
		if m.currentMatch < 0 {
			m.currentMatch = len(displayed) - 1
		}
		m.cursor = m.currentMatch
		m.updateViewportContent()
		m.ensureCursorVisible()
	}
}

// clearSearch clears the current search
func (m *Model) clearSearch() {
	m.searchQuery = ""
	m.searchMatches = []int{}
	m.searchInput.SetValue("")
	m.updateViewportContent()
}

// scrollHalfPageDown scrolls viewport half page down
func (m *Model) scrollHalfPageDown() {
	halfPage := m.viewport.Height / 2
	m.viewport.SetYOffset(m.viewport.YOffset + halfPage)
}

// scrollHalfPageUp scrolls viewport half page up
func (m *Model) scrollHalfPageUp() {
	halfPage := m.viewport.Height / 2
	newOffset := m.viewport.YOffset - halfPage
	if newOffset < 0 {
		newOffset = 0
	}
	m.viewport.SetYOffset(newOffset)
}

// handleGKey handles the g key for gg navigation
func (m *Model) handleGKey() {
	if m.pendingG {
		m.cursor = 0
		m.updateViewportContent()
		m.viewport.GotoTop()
		m.pendingG = false
	} else {
		m.pendingG = true
	}
}

// gotoBottom moves cursor to the last visible resource and scrolls so it's visible
func (m *Model) gotoBottom() {
	displayed := m.displayedResourceIndices()
	if len(displayed) > 0 {
		m.cursor = len(displayed) - 1
	}
	m.updateViewportContent()
	m.ensureCursorVisible()
	m.pendingG = false
}

// fuzzyMatch returns true if all characters in query appear in text in order
// (not necessarily consecutive). E.g. "lmbda" matches "lambda", "inst" matches "instance".
func fuzzyMatch(text, query string) bool {
	text = strings.ToLower(text)
	query = strings.ToLower(query)
	if query == "" {
		return true
	}
	qi := 0
	for i := 0; i < len(text) && qi < len(query); i++ {
		if text[i] == query[qi] {
			qi++
		}
	}
	return qi == len(query)
}

func (m *Model) performSearch() {
	m.searchMatches = []int{}
	m.currentMatch = 0

	if m.searchQuery == "" {
		return // displayedResourceIndices will show full list
	}

	terms := strings.Fields(strings.ToLower(m.searchQuery))
	if len(terms) == 0 {
		return
	}

	filtered := m.sortedResources()
	for displayIdx, resourceIdx := range filtered {
		r := m.plan.Resources[resourceIdx]
		searchable := strings.ToLower(r.Address + " " + r.Type + " " + r.Name)

		allMatch := true
		for _, term := range terms {
			if !fuzzyMatch(searchable, term) {
				allMatch = false
				break
			}
		}
		if allMatch {
			m.searchMatches = append(m.searchMatches, displayIdx)
		}
	}

	if len(m.searchMatches) > 0 {
		m.cursor = 0 // first item in filtered display
		m.currentMatch = 0
		m.blockCursor = -1
	}
}

func (m *Model) updateViewportContent() {
	if !m.ready {
		return
	}
	m.viewport.SetContent(m.renderResources())
}

// ensureCursorVisible scrolls the viewport to make the current cursor visible
func (m *Model) ensureCursorVisible() {
	if !m.ready {
		return
	}

	if m.cursor < 0 || m.cursor >= len(m.resourceLineStarts) {
		return
	}

	lineNum := m.selectedLineStart
	if lineNum < 0 {
		lineNum = m.resourceLineStarts[m.cursor]
	}

	topLine := m.viewport.YOffset
	bottomLine := topLine + m.viewport.Height - 1

	if lineNum < topLine {
		m.viewport.SetYOffset(lineNum)
	} else if lineNum > bottomLine {
		newOffset := lineNum - m.viewport.Height + 1
		if newOffset < 0 {
			newOffset = 0
		}
		m.viewport.SetYOffset(newOffset)
	}
}

// cursorLineVisible reports whether the current selection is already
// within the viewport's visible line range. Used at list boundaries
// (cursor already on the first/last item) to distinguish "the mouse
// wheel scrolled the view away from the cursor, so this boundary
// keypress should snap back to it" from "the view is already where the
// cursor is, so this keypress means free-scroll past the edge."
func (m *Model) cursorLineVisible() bool {
	if !m.ready || m.cursor < 0 || m.cursor >= len(m.resourceLineStarts) {
		return true
	}
	lineNum := m.selectedLineStart
	if lineNum < 0 {
		lineNum = m.resourceLineStarts[m.cursor]
	}
	topLine := m.viewport.YOffset
	bottomLine := topLine + m.viewport.Height - 1
	return lineNum >= topLine && lineNum <= bottomLine
}

// scrollForExpanded ensures the cursor is visible and, when expanded,
// positions the cursor near the top so the expanded content is visible below.
func (m *Model) scrollForExpanded() {
	if !m.ready || m.cursor < 0 || m.cursor >= len(m.resourceLineStarts) {
		return
	}

	lineNum := m.resourceLineStarts[m.cursor]
	filtered := m.sortedResources()
	resourceIdx := -1
	if m.cursor < len(filtered) {
		resourceIdx = filtered[m.cursor]
	}

	if resourceIdx >= 0 && m.expanded[resourceIdx] {
		var endLine int
		if m.cursor+1 < len(m.resourceLineStarts) {
			endLine = m.resourceLineStarts[m.cursor+1]
		} else {
			endLine = m.contentLineCount
		}

		bottomLine := m.viewport.YOffset + m.viewport.Height - 1
		if endLine > bottomLine {
			m.viewport.SetYOffset(lineNum)
			return
		}
	}

	m.ensureCursorVisible()
}

func (m *Model) renderResources() string {
	var b strings.Builder
	lineCount := 0

	displayed := m.displayedResourceIndices()
	m.resourceLineStarts = make([]int, len(displayed))

	if len(displayed) == 0 {
		if m.searchQuery != "" {
			b.WriteString(mutedColor.Render(fmt.Sprintf("No resources match search '%s'. Press Esc to clear.", m.searchQuery)))
		} else {
			b.WriteString(mutedColor.Render("No resources match the current filters. Press 'f' to change filters."))
		}
		b.WriteString("\n")
		return b.String()
	}

	m.selectedLineStart = 0
	for displayIdx, resourceIdx := range displayed {
		m.resourceLineStarts[displayIdx] = lineCount
		r := m.plan.Resources[resourceIdx]

		isSelected := displayIdx == m.cursor
		isExpanded := m.expanded[resourceIdx]
		isMatch := m.searchQuery != "" // when filtering, all displayed items match
		if isSelected && m.blockCursor < 0 {
			m.selectedLineStart = lineCount
		}

		if isSelected {
			line := m.renderSelectedResourceLine(r, isExpanded, isMatch)
			b.WriteString(line)
		} else {
			line := m.renderResourceLine(r, isExpanded, isMatch)
			b.WriteString(line)
		}
		b.WriteString("\n")
		lineCount++

		if isExpanded && len(r.Attributes) > 0 {
			foldIdx := 0
			m.renderAttributeTree(&b, r.Address, r.Attributes, 0, true, isSelected && m.blockCursor >= 0, &foldIdx, &lineCount)
			b.WriteString("\n")
			lineCount++
		}
	}

	m.contentLineCount = lineCount

	b.WriteString("\n")
	eolStyle := lipgloss.NewStyle().Foreground(mutedColorVal)
	b.WriteString(eolStyle.Render("── End of Plan ──"))
	b.WriteString("\n")

	// Padding after the marker so the viewport has room to scroll
	// the last resource's expanded content fully into view
	for i := 0; i < m.viewport.Height; i++ {
		b.WriteString("\n")
	}

	return b.String()
}

const defaultCollapsedFoldLines = 30

// foldBlock is one collapsible container attribute (a map or list with
// children) within a resource's attribute tree.
type foldBlock struct {
	Key   string // address + "#" + Path; stable across re-renders
	Path  string
	Attr  tfplan.Attribute
	Depth int
}

func foldKey(address, path string) string {
	return address + "#" + path
}

// isDescendantPath reports whether path is nested under ancestorPath
// (a dotted attribute path or bracketed list-index path).
func isDescendantPath(path, ancestorPath string) bool {
	return strings.HasPrefix(path, ancestorPath+".") || strings.HasPrefix(path, ancestorPath+"[")
}

// isContainerAttr reports whether an attribute is a non-empty, non-sensitive
// map or list — i.e. something that renders as a foldable block with
// nested children.
func isContainerAttr(a tfplan.Attribute) bool {
	return (a.Kind == tfplan.KindMap || a.Kind == tfplan.KindList) && len(a.Children) > 0 && !a.Sensitive
}

// isFoldableAttr reports whether an attribute renders as a foldable block
// at all — either a container (nested children) or a multi-line string
// (diffed content with no children of its own). Both get an
// expand/collapse indicator and a default-collapse-by-size heuristic;
// only containers recurse into Children.
func isFoldableAttr(a tfplan.Attribute) bool {
	return isContainerAttr(a) || isMultilineStringAttr(a)
}

// countRenderedLines estimates how many lines a foldable attribute would
// take fully expanded — open+close plus recursive descendants for a
// container, or open+close plus diffed line count for a multi-line
// string — used to decide the default collapsed/expanded state.
func countRenderedLines(a tfplan.Attribute) int {
	if isMultilineStringAttr(a) {
		return multilineLineEstimate(a) + 2
	}
	if !isContainerAttr(a) {
		return 1
	}
	n := 2
	for _, c := range a.Children {
		n += countRenderedLines(c)
	}
	return n
}

// multilineLineEstimate returns the larger of the old/new value's line
// count, for sizing the default-collapse heuristic and the collapsed
// "... (N lines)" summary — doesn't need to be exact, just representative.
func multilineLineEstimate(a tfplan.Attribute) int {
	count := func(v any) int {
		s, ok := v.(string)
		if !ok {
			return 0
		}
		return strings.Count(s, "\n") + 1
	}
	oldLines, newLines := count(a.Old), count(a.New)
	if oldLines > newLines {
		return oldLines
	}
	return newLines
}

// allFoldableAttributes returns every foldable attribute in the tree,
// structurally, regardless of current fold state. Used by "expand/collapse
// scope" operations, which must reach descendants even when an
// intermediate ancestor happens to be currently collapsed.
func allFoldableAttributes(address string, attrs []tfplan.Attribute, depth int) []foldBlock {
	var blocks []foldBlock
	for _, a := range attrs {
		if !isFoldableAttr(a) {
			continue
		}
		blocks = append(blocks, foldBlock{Key: foldKey(address, a.Path), Path: a.Path, Attr: a, Depth: depth})
		if isContainerAttr(a) {
			blocks = append(blocks, allFoldableAttributes(address, a.Children, depth+1)...)
		}
	}
	return blocks
}

// flattenFoldableAttributes returns the fold blocks currently visible:
// like allFoldableAttributes, but stops descending into a container once
// it's collapsed, matching what renderAttributeTree actually draws.
func flattenFoldableAttributes(address string, attrs []tfplan.Attribute, depth int, isCollapsed func(key string, size int) bool) []foldBlock {
	var blocks []foldBlock
	for _, a := range attrs {
		if !isFoldableAttr(a) {
			continue
		}
		key := foldKey(address, a.Path)
		blocks = append(blocks, foldBlock{Key: key, Path: a.Path, Attr: a, Depth: depth})
		if isContainerAttr(a) && !isCollapsed(key, countRenderedLines(a)) {
			blocks = append(blocks, flattenFoldableAttributes(address, a.Children, depth+1, isCollapsed)...)
		}
	}
	return blocks
}

// resolveCollapsed looks up an explicit fold-state override, falling back
// to the default-collapse-by-size heuristic.
func (m *Model) resolveCollapsed(key string, size int) bool {
	if collapsed, ok := m.foldedBlocks[key]; ok {
		return collapsed
	}
	return size >= defaultCollapsedFoldLines
}

func (m *Model) isFoldCollapsed(block foldBlock) bool {
	return m.resolveCollapsed(block.Key, countRenderedLines(block.Attr))
}

// renderFoldHeader renders a fold block's header row: the expand/collapse
// indicator, diff-action symbol, and "name = <opener>" (or just <opener>
// when keyed is false, i.e. a positional list element), with
// collapsedSummary appended when collapsed. Shared between container
// attributes ("{"/"[" ... "N attrs/items }") and multi-line string
// attributes ("<<EOT" ... "N lines") — the two foldable attribute shapes.
func (m Model) renderFoldHeader(indent string, attr tfplan.Attribute, keyed, collapsed, selected bool, maxWidth int, opener, collapsedSummary string) string {
	indicator := expandedIndicator
	if collapsed {
		indicator = collapsedIndicator
	}

	var content string
	if keyed {
		content = fastAttrName.Render(attr.Name) + " = " + fastMuted.Render(opener)
	} else {
		content = fastMuted.Render(opener)
	}

	result := indent + indicator + " " + actionPrefixSymbol(attr.Action) + " " + content
	if collapsed {
		result += fastMuted.Render(" ... " + collapsedSummary)
	}

	if !selected {
		return result
	}

	targetWidth := m.width - 4
	if targetWidth <= 0 {
		targetWidth = maxWidth
	}
	plainLen := utf8.RuneCountInString(stripANSI(result))
	if targetWidth > 0 && plainLen < targetWidth {
		result += strings.Repeat(" ", targetWidth-plainLen)
	}
	return lipgloss.NewStyle().Background(selectedBg).Foreground(textColor).Render(result)
}

// containerFoldSummary formats the "N attrs }" / "N items ]" text shown
// after a collapsed container's opening bracket.
func containerFoldSummary(attr tfplan.Attribute) string {
	_, closeBracket := containerBrackets(attr.Kind)
	noun := "attrs"
	if attr.Kind == tfplan.KindList {
		noun = "items"
	}
	return fmt.Sprintf("%d %s %s", len(attr.Children), noun, closeBracket)
}

func closingBraceLine(indent string, kind tfplan.ValueKind) string {
	_, closeBracket := containerBrackets(kind)
	return indent + fastMuted.Render(closeBracket)
}

// multilineFoldSummary formats the "N lines" text shown after a
// collapsed multi-line string's opening "<<EOT" marker.
func multilineFoldSummary(attr tfplan.Attribute) string {
	n := multilineLineEstimate(attr)
	noun := "line"
	if n != 1 {
		noun = "lines"
	}
	return fmt.Sprintf("%d %s", n, noun)
}

// unchangedHiddenNote formats the "# (N unchanged attributes hidden)"
// summary line for a run of no-op attributes, matching Terraform CLI's
// own wording (singular "attribute" for exactly one).
func unchangedHiddenNote(count int) string {
	noun := "attribute"
	if count != 1 {
		noun = "attributes"
	}
	return fmt.Sprintf("# (%d unchanged %s hidden)", count, noun)
}

// renderDiffLines writes context-diff lines into a builder, handling all
// DiffOp types including DiffSeparator for collapsed equal runs. Format
// agnostic over []string, so it's reused unchanged from the old
// heredoc/text-diff renderer for both multiline-string and userdata diffs.
func renderDiffLines(b *strings.Builder, diff []DiffLine, indent string, maxWidth int) {
	for _, d := range diff {
		switch d.Op {
		case DiffSeparator:
			b.WriteString(indent)
			b.WriteString(fastMuted.Render("@@ ··· @@"))
			b.WriteString("\n")
		case DiffDelete:
			wrapped := wrapText(d.Text, maxWidth-len(indent)-4)
			for _, wl := range strings.Split(wrapped, "\n") {
				b.WriteString(indent)
				b.WriteString(fastDestroy.Render("- " + wl))
				b.WriteString("\n")
			}
		case DiffInsert:
			wrapped := wrapText(d.Text, maxWidth-len(indent)-4)
			for _, wl := range strings.Split(wrapped, "\n") {
				b.WriteString(indent)
				b.WriteString(fastCreate.Render("+ " + wl))
				b.WriteString("\n")
			}
		case DiffEqual:
			wrapped := wrapText(d.Text, maxWidth-len(indent)-4)
			for _, wl := range strings.Split(wrapped, "\n") {
				b.WriteString(indent)
				b.WriteString(fastMuted.Render("  " + wl))
				b.WriteString("\n")
			}
		}
	}
}

// wrapText word-wraps s to width, used by renderDiffLines for long diff lines.
func wrapText(s string, width int) string {
	if width <= 10 {
		return s
	}
	return wordwrap.String(s, width)
}

// isMultilineStringAttr reports whether a leaf string attribute's old or
// new value spans multiple lines (e.g. a Helm values YAML blob or a
// cloud-init script) — these get line-by-line diffing instead of being
// shown as one opaque blob.
func isMultilineStringAttr(attr tfplan.Attribute) bool {
	if attr.Kind != tfplan.KindString {
		return false
	}
	if s, ok := attr.Old.(string); ok && strings.Contains(s, "\n") {
		return true
	}
	if s, ok := attr.New.(string); ok && strings.Contains(s, "\n") {
		return true
	}
	return false
}

// renderMultilineStringBody renders a multi-line string attribute's
// diffed content — no header, no "EOT" footer; the caller wraps this
// with renderFoldHeader/closingBraceLine-equivalent lines so it folds
// like any other collapsible block. Unlike the old heredoc-marker text
// scanning this replaces, there's no marker detection needed at all:
// JSON strings are just strings.
func (m Model) renderMultilineStringBody(attr tfplan.Attribute, indent string, maxWidth int) string {
	var b strings.Builder
	contentIndent := indent + "  "

	if attr.Sensitive {
		b.WriteString(contentIndent)
		b.WriteString(fastSensitive.Render("(sensitive value)"))
		b.WriteString("\n")
		return b.String()
	}
	if attr.Computed {
		b.WriteString(contentIndent)
		b.WriteString(attrComputedStyle.Render("(known after apply)"))
		b.WriteString("\n")
		return b.String()
	}

	oldStr, _ := attr.Old.(string)
	newStr, _ := attr.New.(string)

	switch attr.Action {
	case tfplan.ActionCreate:
		for _, l := range strings.Split(newStr, "\n") {
			b.WriteString(contentIndent)
			b.WriteString(fastCreate.Render("+ " + l))
			b.WriteString("\n")
		}
	case tfplan.ActionDelete:
		for _, l := range strings.Split(oldStr, "\n") {
			b.WriteString(contentIndent)
			b.WriteString(fastDestroy.Render("- " + l))
			b.WriteString("\n")
		}
	case tfplan.ActionNoOp:
		for _, l := range strings.Split(newStr, "\n") {
			b.WriteString(contentIndent)
			b.WriteString(fastMuted.Render("  " + l))
			b.WriteString("\n")
		}
	default: // update
		diff := ComputeDiff(strings.Split(oldStr, "\n"), strings.Split(newStr, "\n"))
		contextDiff := ContextDiff(diff, m.diffContextSize())
		if contextDiff == nil {
			b.WriteString(contentIndent)
			b.WriteString(fastMuted.Render("  (no changes)"))
			b.WriteString("\n")
		} else {
			renderDiffLines(&b, contextDiff, contentIndent, maxWidth)
		}
	}

	return b.String()
}

// tryRenderUserdataAttr detects user_data/user_data_base64 attributes and
// renders them decoded, with the decoded content diffed on change. The
// trigger is now a simple name check on structured data, replacing the
// old " = "-splitting text parse.
func (m Model) tryRenderUserdataAttr(attr tfplan.Attribute, indent string, maxWidth int) (string, bool) {
	if attr.Kind != tfplan.KindString || attr.Sensitive {
		return "", false
	}
	if attr.Name != "user_data" && attr.Name != "user_data_base64" {
		return "", false
	}

	oldB64, _ := attr.Old.(string)
	newB64, _ := attr.New.(string)
	oldDecoded, oldOk := TryDecodeUserdata(oldB64)
	newDecoded, newOk := TryDecodeUserdata(newB64)
	if !oldOk && !newOk {
		return "", false
	}

	decodedIndent := indent + "  "
	var b strings.Builder
	b.WriteString(indent + actionPrefixSymbol(attr.Action) + " " + renderKeyValue(attr, true))
	b.WriteString("\n")
	b.WriteString(decodedIndent)
	b.WriteString(fastMuted.Render("┄┄┄ decoded " + attr.Name + " ┄┄┄"))
	b.WriteString("\n")

	switch {
	case oldOk && newOk:
		diff := ComputeDiff(strings.Split(oldDecoded, "\n"), strings.Split(newDecoded, "\n"))
		contextDiff := ContextDiff(diff, m.diffContextSize())
		if contextDiff == nil {
			b.WriteString(decodedIndent)
			b.WriteString(fastMuted.Render("  (no changes in decoded content)"))
			b.WriteString("\n")
		} else {
			renderDiffLines(&b, contextDiff, decodedIndent, maxWidth)
		}
	case oldOk:
		for _, l := range strings.Split(oldDecoded, "\n") {
			b.WriteString(decodedIndent)
			b.WriteString(lipgloss.NewStyle().Foreground(destroyColor).Render("- " + l))
			b.WriteString("\n")
		}
	case newOk:
		for _, l := range strings.Split(newDecoded, "\n") {
			b.WriteString(decodedIndent)
			b.WriteString(lipgloss.NewStyle().Foreground(createColor).Render("+ " + l))
			b.WriteString("\n")
		}
	}
	b.WriteString(decodedIndent)
	b.WriteString(fastMuted.Render("┄┄┄ end " + attr.Name + " ┄┄┄"))
	return b.String(), true
}

// renderLeafRow renders one "name = value" (or bare "value") row for a
// plain scalar attribute, word-wrapping long create/delete/unchanged
// values (update rows, with their old → new arrow, are left unwrapped —
// splitting two colored segments across lines isn't worth the added
// complexity for what's a cosmetic nicety).
func (m Model) renderLeafRow(indent string, attr tfplan.Attribute, keyed bool, maxWidth int) string {
	prefix := actionPrefixSymbol(attr.Action)
	rowPrefix := indent + prefix + " "
	full := rowPrefix + renderKeyValue(attr, keyed)

	if maxWidth <= 0 || attr.Action == tfplan.ActionUpdate || attr.Sensitive || attr.Computed || !keyed {
		return full
	}

	plainValue := renderScalarText(attr.New)
	styled := attrNewValueStyle
	if attr.Action == tfplan.ActionDelete {
		plainValue = renderScalarText(attr.Old)
		styled = attrOldValueStyle
	}

	available := maxWidth - utf8.RuneCountInString(rowPrefix) - utf8.RuneCountInString(attr.Name) - 3
	if available < 20 || utf8.RuneCountInString(plainValue) <= available {
		return full
	}

	wrapped := wordwrap.String(plainValue, available)
	subLines := strings.Split(wrapped, "\n")
	if len(subLines) <= 1 {
		return full
	}

	continuation := indent + strings.Repeat(" ", utf8.RuneCountInString(prefix)+1)
	var b strings.Builder
	for i, sub := range subLines {
		if i > 0 {
			b.WriteString("\n")
			b.WriteString(continuation)
		} else {
			b.WriteString(rowPrefix)
			b.WriteString(fastAttrName.Render(attr.Name))
			b.WriteString(" = ")
		}
		b.WriteString(styled.Render(sub))
	}
	return b.String()
}

// renderAttributeTree recursively renders a resource's (or a container
// attribute's) children, applying fold state, multiline-string diffing,
// userdata decoding, and sensitive/computed styling. keyed controls
// whether each attribute prints its own "name = " prefix: true for
// resource-level attributes and map entries, false for positional list
// elements. foldIdx and lineCount are threaded through so the caller can
// track blockCursor position and total rendered line count exactly as
// before.
//
// Terraform's JSON plan always carries the resource's full before/after
// state, not just the diff, so most attributes of a partially-changed
// resource are unchanged context rather than part of the actual change.
// Showing every one of them individually — even muted — buries the real
// diff and is exactly what confused review of real-world plans. Terraform
// CLI's own text renderer solves this by collapsing runs of unchanged
// attributes into a single "# (N unchanged attributes hidden)" note; this
// mirrors that convention rather than rendering them one by one.
func (m *Model) renderAttributeTree(b *strings.Builder, address string, attrs []tfplan.Attribute, depth int, keyed bool, selected bool, foldIdx *int, lineCount *int) {
	maxWidth := m.viewport.Width
	indent := strings.Repeat("  ", depth)

	for i := 0; i < len(attrs); i++ {
		attr := attrs[i]

		if attr.Action == tfplan.ActionNoOp {
			run := 1
			for i+run < len(attrs) && attrs[i+run].Action == tfplan.ActionNoOp {
				run++
			}
			b.WriteString(indent + fastMuted.Render(unchangedHiddenNote(run)))
			b.WriteString("\n")
			*lineCount++
			i += run - 1
			continue
		}

		if keyed {
			if decoded, ok := m.tryRenderUserdataAttr(attr, indent, maxWidth); ok {
				b.WriteString(decoded)
				b.WriteString("\n")
				*lineCount += strings.Count(decoded, "\n") + 1
				continue
			}
		}

		if isContainerAttr(attr) {
			key := foldKey(address, attr.Path)
			collapsed := m.resolveCollapsed(key, countRenderedLines(attr))
			blockSelected := selected && *foldIdx == m.blockCursor
			if blockSelected {
				m.selectedLineStart = *lineCount
			}
			open, _ := containerBrackets(attr.Kind)
			b.WriteString(m.renderFoldHeader(indent, attr, keyed, collapsed, blockSelected, maxWidth, open, containerFoldSummary(attr)))
			b.WriteString("\n")
			*lineCount++
			*foldIdx++

			if !collapsed {
				childKeyed := attr.Kind == tfplan.KindMap
				m.renderAttributeTree(b, address, attr.Children, depth+1, childKeyed, selected, foldIdx, lineCount)
				b.WriteString(closingBraceLine(indent, attr.Kind))
				b.WriteString("\n")
				*lineCount++
			}
			continue
		}

		if attr.Sensitive {
			row := indent + actionPrefixSymbol(attr.Action) + " " + renderKeyValue(attr, keyed)
			b.WriteString(row)
			b.WriteString("\n")
			*lineCount++
			continue
		}

		if isMultilineStringAttr(attr) {
			key := foldKey(address, attr.Path)
			collapsed := m.resolveCollapsed(key, countRenderedLines(attr))
			blockSelected := selected && *foldIdx == m.blockCursor
			if blockSelected {
				m.selectedLineStart = *lineCount
			}
			b.WriteString(m.renderFoldHeader(indent, attr, keyed, collapsed, blockSelected, maxWidth, "<<EOT", multilineFoldSummary(attr)))
			b.WriteString("\n")
			*lineCount++
			*foldIdx++

			if !collapsed {
				body := m.renderMultilineStringBody(attr, indent, maxWidth)
				b.WriteString(body)
				*lineCount += strings.Count(body, "\n")
				b.WriteString(indent + fastMuted.Render("EOT"))
				b.WriteString("\n")
				*lineCount++
			}
			continue
		}

		row := m.renderLeafRow(indent, attr, keyed, maxWidth)
		b.WriteString(row)
		b.WriteString("\n")
		*lineCount += strings.Count(row, "\n") + 1
	}
}

// renderSelectedResourceLine renders a resource line with full-width background highlight
func (m Model) renderSelectedResourceLine(r tfplan.Resource, expanded bool, _ bool) string {
	// Build the line content
	var content strings.Builder

	// Expand/collapse indicator
	if expanded {
		content.WriteString("▼")
	} else {
		content.WriteString("▶")
	}
	content.WriteString(" ")

	// Action symbol
	switch r.Action {
	case tfplan.ActionCreate:
		content.WriteString("+")
	case tfplan.ActionDelete:
		content.WriteString("-")
	case tfplan.ActionUpdate:
		content.WriteString("~")
	case tfplan.ActionReplace:
		content.WriteString("±")
	case tfplan.ActionRead:
		content.WriteString("≤")
	case tfplan.ActionForget:
		content.WriteString("⊘")
	default:
		content.WriteString("~")
	}
	content.WriteString(" ")

	// Resource address
	content.WriteString(r.Address)

	// Action description
	actionDesc := getActionDescription(r)
	content.WriteString(" ")
	content.WriteString(actionDesc)

	// Change count
	if n := r.ChangedAttributeCount(); n > 0 {
		fmt.Fprintf(&content, " (%d changes)", n)
	}

	// Pad to full width and apply selected style with foreground color
	line := content.String()
	targetWidth := m.width - 4
	if targetWidth > 0 && len(line) < targetWidth {
		line = line + strings.Repeat(" ", targetWidth-len(line))
	}

	// Apply style with both foreground and background
	actionStyle := lipgloss.NewStyle().
		Background(selectedBg).
		Foreground(GetActionColor(string(r.Action))).
		Bold(true)

	return actionStyle.Render(line)
}

func (m Model) renderResourceLine(r tfplan.Resource, expanded bool, isMatch bool) string {
	var b strings.Builder

	// Expand/collapse indicator
	if expanded {
		b.WriteString(expandedIndicator)
	} else {
		b.WriteString(collapsedIndicator)
	}
	b.WriteString(" ")

	// Action symbol
	b.WriteString(GetActionSymbol(string(r.Action)))
	b.WriteString(" ")

	// Resource address
	style := GetResourceStyle(string(r.Action))
	address := r.Address

	if isMatch && m.searchQuery != "" {
		// Highlight matching text
		address = highlightMatch(address, m.searchQuery)
	}

	b.WriteString(style.Render(address))

	// Action description
	actionDesc := getActionDescription(r)
	b.WriteString(" ")
	b.WriteString(fastMuted.Render(actionDesc))

	// Change count for expanded content
	if n := r.ChangedAttributeCount(); n > 0 {
		b.WriteString(fastMuted.Render(fmt.Sprintf(" (%d changes)", n)))
	}

	return b.String()
}

func highlightMatch(text, query string) string {
	lower := strings.ToLower(text)
	lowerQuery := strings.ToLower(query)

	idx := strings.Index(lower, lowerQuery)
	if idx == -1 {
		return text
	}

	before := text[:idx]
	match := text[idx : idx+len(query)]
	after := text[idx+len(query):]

	return before + matchStyle.Render(match) + after
}

// getActionDescription returns the human-readable description shown next
// to a resource's address, distinguishing the two replace orderings via
// ReplacePattern the way the legacy delete-create/create-delete actions
// used to.
func getActionDescription(r tfplan.Resource) string {
	switch r.Action {
	case tfplan.ActionCreate:
		return "will be created"
	case tfplan.ActionDelete:
		return "will be destroyed"
	case tfplan.ActionUpdate:
		return "will be updated"
	case tfplan.ActionReplace:
		if r.ReplacePattern == tfplan.ReplaceCreateBeforeDestroy {
			return "will be created and then destroyed"
		}
		return "will be destroyed and then created"
	case tfplan.ActionRead:
		return "will be read"
	case tfplan.ActionForget:
		return "will be removed from state"
	case tfplan.ActionOutput:
		return "output values will change"
	default:
		return ""
	}
}

// sortOrderLabel returns a display label for a sort option
func sortOrderLabel(opt SortOrder) string {
	switch opt {
	case SortDefault:
		return "default (plan order)"
	case SortByAction:
		return "by action"
	case SortByAddress:
		return "by address"
	case SortByType:
		return "by type"
	default:
		return string(opt)
	}
}

// sortOrderHint returns a one-line hint explaining what a sort option does
func sortOrderHint(opt SortOrder) string {
	switch opt {
	case SortDefault:
		return "— as Terraform outputs them"
	case SortByAction:
		return "— group create, destroy, update, etc."
	case SortByAddress:
		return "— alphabetical by resource address"
	case SortByType:
		return "— group by resource type (aws_instance, etc.)"
	default:
		return ""
	}
}

// filterActionLabel returns a short label for the filter picker
func filterActionLabel(action tfplan.Action) string {
	switch action {
	case tfplan.ActionCreate:
		return "create"
	case tfplan.ActionDelete:
		return "destroy"
	case tfplan.ActionUpdate:
		return "update"
	case tfplan.ActionReplace:
		return "replace"
	case tfplan.ActionRead:
		return "read"
	case tfplan.ActionForget:
		return "forget"
	case tfplan.ActionOutput:
		return "output"
	default:
		return string(action)
	}
}

// viewFilterPicker renders the filter picker overlay (returns full view, caller returns early).
func (m Model) viewFilterPicker() string {
	var b strings.Builder
	b.WriteString(searchStyle.Render("Filter by status (Space: toggle, a: all, c: clear, Enter: apply, Esc: clear all and close)"))
	b.WriteString("\n\n")
	for i, action := range filterableActions {
		checked := "[ ]"
		if m.statusFilters != nil && m.statusFilters[action] {
			checked = "[x]"
		}
		label := filterActionLabel(action)
		rowStyle := lipgloss.NewStyle().Foreground(textColor)
		if i == m.filterCursor {
			rowStyle = rowStyle.Background(selectedBg)
		}
		labelStyle := GetResourceStyle(string(action))
		b.WriteString(rowStyle.Render("  "+checked+" ") + labelStyle.Render(label))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("j/k: navigate • Space: toggle • a: select all • c: clear all • Enter: apply • Esc: clear all and close"))
	return appStyle.Render(b.String())
}

// viewSortPicker renders the sort picker overlay (returns full view, caller returns early).
func (m Model) viewSortPicker() string {
	var b strings.Builder
	b.WriteString(searchStyle.Render("Sort by (Enter/Space: select, Esc: close)"))
	b.WriteString("\n\n")
	for i, opt := range sortOptions {
		marker := "  "
		if opt == m.sortOrder {
			marker = "● "
		}
		rowStyle := lipgloss.NewStyle().Foreground(textColor)
		if i == m.sortCursor {
			rowStyle = rowStyle.Background(selectedBg)
		}
		line := marker + sortOrderLabel(opt) + " " + mutedColor.Render(sortOrderHint(opt))
		b.WriteString(rowStyle.Render(line))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("j/k: navigate • Enter/Space: select • Esc: close"))
	return appStyle.Render(b.String())
}

// viewHeader renders the header and summary.
func (m Model) viewHeader() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("🔺 Terra-Prism - Terraform Plan Viewer"))
	b.WriteString("\n")
	summary := fmt.Sprintf("  %s to add, %s to change, %s to destroy",
		lipgloss.NewStyle().Foreground(createColor).Render(fmt.Sprintf("%d", m.plan.TotalAdd)),
		lipgloss.NewStyle().Foreground(updateColor).Render(fmt.Sprintf("%d", m.plan.TotalChange)),
		lipgloss.NewStyle().Foreground(destroyColor).Render(fmt.Sprintf("%d", m.plan.TotalDestroy)),
	)
	if m.plan.OutputCount > 0 {
		summary += fmt.Sprintf(", %s output(s) changed",
			lipgloss.NewStyle().Foreground(updateColor).Render(fmt.Sprintf("%d", m.plan.OutputCount)),
		)
	}
	b.WriteString(summaryStyle.Render(summary))
	b.WriteString("\n\n")
	return b.String()
}

// viewFilterStatus renders the filter status line when filters are active.
func (m Model) viewFilterStatus() string {
	if len(m.statusFilters) == 0 {
		return ""
	}
	var labels []string
	for _, action := range filterableActions {
		if m.statusFilters[action] {
			labels = append(labels, filterActionLabel(action))
		}
	}
	return searchStyle.Render(fmt.Sprintf("Filter: %s (%d active) • f: change • Esc: clear all", strings.Join(labels, ", "), len(labels))) + "\n\n"
}

// viewSortStatus renders the sort status line when not default.
func (m Model) viewSortStatus() string {
	if m.sortOrder == SortDefault || m.sortOrder == "" {
		return ""
	}
	return searchStyle.Render(fmt.Sprintf("Sort: %s • s: change", sortOrderLabel(m.sortOrder))) + "\n\n"
}

// viewSearchBar renders the search bar or match info.
func (m Model) viewSearchBar() string {
	if m.searching {
		return searchStyle.Render("Search: ") + m.searchInput.View() + "\n\n"
	}
	if m.searchQuery != "" {
		return searchStyle.Render(fmt.Sprintf("Search: %q (%d/%d matches)", m.searchQuery, m.currentMatch+1, len(m.searchMatches))) + "\n\n"
	}
	return ""
}

// viewConfirmationPrompt renders the apply confirmation prompt.
func (m Model) viewConfirmationPrompt() string {
	if !m.confirmApply {
		return ""
	}
	confirmStyle := lipgloss.NewStyle().
		Background(destroyColor).
		Foreground(textColor).
		Bold(true).
		Padding(0, 2)
	return "\n" + confirmStyle.Render("⚠️  Apply this plan? Press 'y' to confirm, any other key to cancel") + "\n\n"
}

// viewHelpFooter returns the help footer text.
func (m Model) viewHelpFooter() string {
	maxWidth := m.width - 4
	if maxWidth <= 0 {
		maxWidth = m.viewport.Width
	}

	if m.applyMode {
		if m.confirmApply {
			return "y: confirm apply • any key: cancel"
		}
		applyHint := lipgloss.NewStyle().Foreground(createColor).Bold(true).Render("a: APPLY")
		full := fmt.Sprintf("%s • j/k/↑↓: navigate • e/c: scope • E/C: all • /: search • f: filter • s: sort • q: quit", applyHint)
		if lipgloss.Width(full) <= maxWidth {
			return full
		}
		medium := fmt.Sprintf("%s • j/k nav • e/c scope • E/C all • / search • q", applyHint)
		if lipgloss.Width(medium) <= maxWidth {
			return medium
		}
		return fmt.Sprintf("%s • j/k nav • e/c • / search • q", applyHint)
	}

	helpOptions := []string{
		"j/k/↑↓: navigate • l/→: expand • h/←/⌫: collapse • e/c: scope • E/C: all • +/-: diff context • Ctrl+E/Y: line scroll • d/u: page scroll • gg/G: top/bottom • /: search • f: filter • s: sort • q: quit",
		"j/k: nav • l/h: fold • e/c: scope • E/C: all • +/-: diff ctx • Ctrl+E/Y: line • d/u: page • /: search • f/s • q",
		"j/k nav • l/h fold • e/c scope • E/C all • +/- diff • Ctrl+E/Y scroll • / search • q",
		"j/k nav • l/h fold • e/c • q",
	}

	if len(m.statusFilters) > 0 {
		for i, help := range helpOptions {
			helpOptions[i] = help + " • Esc clears filter"
		}
	}

	for _, help := range helpOptions {
		if lipgloss.Width(help) <= maxWidth {
			return help
		}
	}
	return helpOptions[len(helpOptions)-1]
}

// viewUpdateNudge renders the update available nudge.
func (m Model) viewUpdateNudge() string {
	if m.updateAvailable == "" {
		return ""
	}
	nudgeStyle := lipgloss.NewStyle().Foreground(computedColor).Italic(true)
	return "\n" + nudgeStyle.Render(fmt.Sprintf("Update available: v%s. Run 'terraprism upgrade' to update.", m.updateAvailable))
}

// View renders the UI
func (m Model) View() string {
	if !m.ready {
		return "Loading..."
	}
	if m.filtering {
		return m.viewFilterPicker()
	}
	if m.sorting {
		return m.viewSortPicker()
	}

	var b strings.Builder
	b.WriteString(m.viewHeader())
	b.WriteString(m.viewFilterStatus())
	b.WriteString(m.viewSortStatus())
	b.WriteString(m.viewSearchBar())
	b.WriteString(m.viewConfirmationPrompt())
	b.WriteString(m.viewport.View())
	b.WriteString("\n")
	b.WriteString(helpStyle.Render(m.viewHelpFooter()))
	b.WriteString(m.viewUpdateNudge())
	return appStyle.Render(b.String())
}
