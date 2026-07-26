package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/CaptShanks/terraprism/internal/tfplan"
)

func simplePlan() *tfplan.Plan {
	return &tfplan.Plan{Resources: []tfplan.Resource{
		{Address: "null_resource.a", Action: tfplan.ActionCreate},
	}}
}

func TestOutputToggleNoOpWithoutPlanOutput(t *testing.T) {
	m := NewModelWithApply(simplePlan(), "/tmp/plan", "terraform", "", "")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	mm := model.(Model)

	model, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	mm = model.(Model)

	if mm.outputPane.Visible() {
		t.Fatalf("expected 'o' to be a no-op when there's no captured plan output")
	}
}

func TestOutputTogglePreAndPostApply(t *testing.T) {
	planText := "Plan: 1 to add, 0 to change, 0 to destroy.\nsome plan output line"
	m := NewModelWithApply(simplePlan(), "/tmp/plan", "terraform", "", planText)
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	mm := model.(Model)

	if mm.outputPane.Visible() {
		t.Fatalf("output pane should start hidden")
	}

	model, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	mm = model.(Model)
	if !mm.outputPane.Visible() {
		t.Fatalf("expected 'o' to show the output pane when plan output is available")
	}
	view := mm.View()
	if !strings.Contains(view, "some plan output line") {
		t.Fatalf("expected the captured plan output visible in the composed view:\n%s", view)
	}

	model, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	mm = model.(Model)
	if mm.outputPane.Visible() {
		t.Fatalf("expected a second 'o' press to hide the pane again")
	}
}

func TestReflowGivesOutputPaneMostOfTheScreen(t *testing.T) {
	m := NewModelWithApply(simplePlan(), "/tmp/plan", "terraform", "", "some output")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	mm := model.(Model)

	fullTreeHeight := mm.treeView.Height()

	model, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	mm = model.(Model)

	if mm.treeView.Height() != treeHeightWithOutputVisible {
		t.Fatalf("expected treeView to shrink to the fixed context height (%d) once the pane is shown, got %d",
			treeHeightWithOutputVisible, mm.treeView.Height())
	}
	if mm.treeView.Height()+mm.outputPane.Height() != fullTreeHeight {
		t.Fatalf("expected treeView height + outputPane height to equal the pre-toggle full height (%d), got %d+%d",
			fullTreeHeight, mm.treeView.Height(), mm.outputPane.Height())
	}
	if mm.outputPane.Height() <= mm.treeView.Height() {
		t.Fatalf("expected the output pane to take up most of the screen, got pane=%d tree=%d", mm.outputPane.Height(), mm.treeView.Height())
	}
}

// pressKeyOn drives one key through the full Model.Update() pipeline,
// exactly like a real keystroke, and returns the updated Model.
func pressKeyOn(m Model, key string) Model {
	var msg tea.KeyMsg
	switch key {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	updated, _ := m.Update(msg)
	return updated.(Model)
}

func TestOutputPaneReceivesNavigationAndSearchWhileVisible(t *testing.T) {
	lines := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		lines = append(lines, "line "+string(rune('a'+i%26)))
	}
	lines[20] = "the needle is here"
	planText := strings.Join(lines, "\n")

	m := NewModelWithApply(simplePlan(), "/tmp/plan", "terraform", "", planText)
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	mm := model.(Model)

	mm = pressKeyOn(mm, "o") // show the pane
	if !mm.outputPane.Visible() {
		t.Fatalf("setup: expected the pane visible")
	}

	// Navigation keys go to the pane, not the tree, while it's visible.
	treeSelectedBefore, _ := mm.treeView.State().SelectedID()
	mm = pressKeyOn(mm, "j")
	treeSelectedAfter, _ := mm.treeView.State().SelectedID()
	if treeSelectedAfter != treeSelectedBefore {
		t.Fatalf("expected 'j' to scroll the output pane, not move the tree's selection")
	}
	if mm.outputPane.AtTop() {
		t.Fatalf("expected 'j' to scroll the pane down from the top")
	}

	mm = pressKeyOn(mm, "g")
	mm = pressKeyOn(mm, "g")
	if !mm.outputPane.AtTop() {
		t.Fatalf("expected 'gg' to jump the pane back to the top")
	}

	// Search within the pane using the same '/' / n / N vocabulary as
	// the tree view.
	mm = pressKeyOn(mm, "/")
	if !mm.outputPane.SearchActive() {
		t.Fatalf("expected '/' to start a search inside the pane")
	}
	for _, r := range "needle" {
		mm = pressKeyOn(mm, string(r))
	}
	mm = pressKeyOn(mm, "enter")
	if mm.outputPane.SearchActive() {
		t.Fatalf("expected enter to close the pane's search input")
	}
	if !strings.Contains(mm.View(), "needle") {
		t.Fatalf("expected the matched line to have scrolled into view")
	}

	// 'o' still closes the pane, and normal tree keys resume afterward.
	mm = pressKeyOn(mm, "o")
	if mm.outputPane.Visible() {
		t.Fatalf("expected 'o' to still close the pane while it has focus")
	}
}

func TestOutputPaneResizedEvenWhileHidden(t *testing.T) {
	m := NewModelWithApply(simplePlan(), "/tmp/plan", "terraform", "", "some output")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	mm := model.(Model)

	// Toggling visible should show correctly-wrapped content immediately,
	// with no stale/zero-size flash -- possible only if the pane was
	// already sized while hidden.
	model, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	mm = model.(Model)
	if !strings.Contains(mm.View(), "some output") {
		t.Fatalf("expected output content to render correctly the instant the pane is shown")
	}
}
