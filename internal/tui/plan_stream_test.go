package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/CaptShanks/terraprism/internal/runner"
)

const validPlanJSONForTUI = `{"format_version":"1.2","terraform_version":"1.7.5","resource_changes":[{"address":"null_resource.x","mode":"managed","type":"null_resource","name":"x","change":{"actions":["create"],"before":null,"after":{}}}],"output_changes":{}}`

const fakePlanScript = `#!/bin/sh
outfile=""
case "$1" in
  plan)
    for arg in "$@"; do
      case "$arg" in
        -out=*) outfile="${arg#-out=}" ;;
      esac
    done
    if [ -n "$outfile" ]; then
      echo "fake-plan-binary" > "$outfile"
    fi
    if [ -n "$FAKE_PLAN_LINES" ]; then
      old_ifs="$IFS"
      IFS='|'
      for line in $FAKE_PLAN_LINES; do
        echo "$line"
      done
      IFS="$old_ifs"
    fi
    exit "${FAKE_PLAN_EXIT:-0}"
    ;;
  show)
    printf '%s' "$FAKE_SHOW_STDOUT"
    exit "${FAKE_SHOW_EXIT:-0}"
    ;;
esac
exit 1
`

func writeFakePlanScript(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-plan")
	if err := os.WriteFile(path, []byte(fakePlanScript), 0o755); err != nil {
		t.Fatalf("writing fake plan script: %v", err)
	}
	return path
}

// driveToMsgType runs m forward, starting from cmd, until a message of
// the same type as want has been processed, and returns the resulting
// model. Mirrors driveApplyToCompletion but generalized since planning
// can end via planDoneMsg regardless of success/failure.
func driveToMsgType(t *testing.T, m Model, cmd tea.Cmd, want tea.Msg) Model {
	t.Helper()
	wantType := func(x tea.Msg) bool {
		switch want.(type) {
		case planDoneMsg:
			_, ok := x.(planDoneMsg)
			return ok
		default:
			t.Fatalf("unsupported want type in test helper")
			return false
		}
	}
	for i := 0; i < 1000; i++ {
		if cmd == nil {
			t.Fatalf("expected a non-nil cmd while planning is still in flight")
		}
		msg := cmd()
		var newModel tea.Model
		newModel, cmd = m.Update(msg)
		m = newModel.(Model)
		if wantType(msg) {
			return m
		}
	}
	t.Fatalf("planning did not complete within 1000 messages")
	return m
}

func TestNewModelPlanningStartsEmptyAndStreamsIntoOutputPane(t *testing.T) {
	fake := writeFakePlanScript(t)
	t.Setenv("FAKE_PLAN_LINES", "reading state...|refreshing...|plan generated")
	t.Setenv("FAKE_SHOW_STDOUT", validPlanJSONForTUI)

	m := NewModelPlanning(runner.Options{Cmd: runner.TFCommand(fake)}, "", true)
	if !m.planning {
		t.Fatalf("expected planning=true immediately after construction")
	}
	if !m.outputPane.Visible() {
		t.Fatalf("expected the output pane to be visible from the start")
	}
	if len(m.plan.Resources) != 0 {
		t.Fatalf("expected an empty plan before streaming completes, got %d resources", len(m.plan.Resources))
	}

	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	mm := model.(Model)

	// While planning, quit is blocked and filter/sort/apply are no-ops.
	_, quitCmd, _ := handleKeyQuit(mm)
	if quitCmd != nil {
		t.Fatalf("expected quit to be blocked while planning")
	}
	afterFilter, _, _ := handleKeyFilter(mm)
	if afterFilter.filtering {
		t.Fatalf("expected 'f' to be a no-op while planning")
	}
	afterApply, _, _ := handleKeyApply(mm)
	if afterApply.confirmApply {
		t.Fatalf("expected 'a' to be a no-op while planning")
	}

	cmd := mm.Init()
	final := driveToMsgType(t, mm, cmd, planDoneMsg{})

	if final.planning {
		t.Fatalf("expected planning=false once the stream completes")
	}
	if final.PlanErr() != nil {
		t.Fatalf("unexpected PlanErr: %v", final.PlanErr())
	}
	if len(final.Plan().Resources) != 1 {
		t.Fatalf("expected the real plan populated after streaming, got %+v", final.Plan())
	}
	if final.outputPane.Visible() {
		t.Fatalf("expected the output pane to hide itself once the tree is ready")
	}

	// The pane still holds the captured plan output, reachable via 'o'.
	toggled, _, _ := handleKeyToggleOutput(final)
	if !toggled.outputPane.Visible() {
		t.Fatalf("expected 'o' to reopen the pane after planning completes")
	}
	view := toggled.View()
	for _, want := range []string{"reading state...", "refreshing...", "plan generated"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected streamed plan line %q preserved in the pane:\n%s", want, view)
		}
	}

	// Quit works again now that planning has finished.
	_, quitCmd2, _ := handleKeyQuit(final)
	if quitCmd2 == nil {
		t.Fatalf("expected quit to work again once planning has finished")
	}
}

func TestNewModelPlanningFailureKeepsEmptyTreeAndAllowsQuit(t *testing.T) {
	fake := writeFakePlanScript(t)
	t.Setenv("FAKE_PLAN_LINES", "reading state...|Error: something went wrong")
	t.Setenv("FAKE_PLAN_EXIT", "1")

	m := NewModelPlanning(runner.Options{Cmd: runner.TFCommand(fake)}, "", true)
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	mm := model.(Model)

	cmd := mm.Init()
	final := driveToMsgType(t, mm, cmd, planDoneMsg{})

	if final.planning {
		t.Fatalf("expected planning=false once the failed stream completes")
	}
	if final.PlanErr() == nil {
		t.Fatalf("expected a non-nil PlanErr for a failed plan")
	}
	if len(final.Plan().Resources) != 0 {
		t.Fatalf("expected the plan to remain empty on failure")
	}
	if !final.outputPane.Visible() {
		t.Fatalf("expected the output pane to stay visible showing the failure")
	}
	view := final.View()
	if !strings.Contains(view, "Plan failed") {
		t.Fatalf("expected a plan-failed banner in the view:\n%s", view)
	}

	_, quitCmd, _ := handleKeyQuit(final)
	if quitCmd == nil {
		t.Fatalf("expected quit to work once a failed plan has finished")
	}
}
