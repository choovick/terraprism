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

func TestReflowSplitsHeightWhenOutputVisible(t *testing.T) {
	m := NewModelWithApply(simplePlan(), "/tmp/plan", "terraform", "", "some output")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	mm := model.(Model)

	fullTreeHeight := mm.treeView.Height()

	model, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	mm = model.(Model)

	if mm.treeView.Height() >= fullTreeHeight {
		t.Fatalf("expected treeView's height to shrink once the output pane is shown: before=%d after=%d", fullTreeHeight, mm.treeView.Height())
	}
	if mm.treeView.Height()+outputPaneHeight != fullTreeHeight {
		t.Fatalf("expected treeView height + outputPaneHeight (%d) to equal the pre-toggle full height (%d), got treeView height %d",
			outputPaneHeight, fullTreeHeight, mm.treeView.Height())
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
