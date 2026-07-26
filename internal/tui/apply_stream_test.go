package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

const fakeApplyScript = `#!/bin/sh
if [ -n "$FAKE_APPLY_LINES" ]; then
  old_ifs="$IFS"
  IFS='|'
  for line in $FAKE_APPLY_LINES; do
    echo "$line"
  done
  IFS="$old_ifs"
fi
exit "${FAKE_APPLY_EXIT:-0}"
`

func writeFakeApplyScript(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-apply")
	if err := os.WriteFile(path, []byte(fakeApplyScript), 0o755); err != nil {
		t.Fatalf("writing fake apply script: %v", err)
	}
	return path
}

// driveApplyToCompletion runs m's already-started apply stream to
// completion by repeatedly invoking whatever tea.Cmd Update returns,
// exactly as the real Bubble Tea runtime would, and returns the final
// model once applyDoneMsg has been processed.
func driveApplyToCompletion(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for i := 0; i < 1000; i++ {
		if cmd == nil {
			t.Fatalf("expected a non-nil cmd while apply is still in flight")
		}
		msg := cmd()
		var newModel tea.Model
		newModel, cmd = m.Update(msg)
		m = newModel.(Model)
		if _, ok := msg.(applyDoneMsg); ok {
			return m
		}
	}
	t.Fatalf("apply did not complete within 1000 messages")
	return m
}

func TestConfirmApplyStartsStreamingAndBlocksQuit(t *testing.T) {
	fake := writeFakeApplyScript(t)
	t.Setenv("FAKE_APPLY_LINES", "creating...|still creating...|apply complete!")

	m := NewModelWithApply(simplePlan(), "/dev/null", fake, "", "")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	mm := model.(Model)
	mm.applyMode = true
	mm.confirmApply = true

	updated, cmd, _ := handleKeyConfirmApply(mm)
	if !updated.applying {
		t.Fatalf("expected applying=true immediately after confirming apply")
	}
	if updated.confirmApply {
		t.Fatalf("expected confirmApply cleared once apply starts")
	}

	// While applying, quit must be a no-op.
	quit, quitCmd, _ := handleKeyQuit(updated)
	if quitCmd != nil {
		t.Fatalf("expected quit to be blocked (nil cmd) while applying")
	}
	if !quit.applying {
		t.Fatalf("blocked quit should not otherwise change state")
	}

	final := driveApplyToCompletion(t, updated, cmd)

	if final.applying {
		t.Fatalf("expected applying=false once the stream completes")
	}
	if !final.applyAttempted {
		t.Fatalf("expected applyAttempted=true")
	}
	if final.applyResult != nil {
		t.Fatalf("expected nil applyResult on success, got %v", final.applyResult)
	}
	if !final.outputPane.Visible() {
		t.Fatalf("expected the output pane to auto-show once apply starts")
	}
	view := final.View()
	for _, want := range []string{"creating...", "still creating...", "apply complete!"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected streamed line %q visible in the output pane:\n%s", want, view)
		}
	}

	// Quit is allowed again now that applying has finished.
	_, quitCmd2, _ := handleKeyQuit(final)
	if quitCmd2 == nil {
		t.Fatalf("expected quit to work again once apply has finished")
	}
}

func TestApplyStreamFailureSurfacesError(t *testing.T) {
	fake := writeFakeApplyScript(t)
	t.Setenv("FAKE_APPLY_EXIT", "1")

	m := NewModelWithApply(simplePlan(), "/dev/null", fake, "", "")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	mm := model.(Model)
	mm.applyMode = true
	mm.confirmApply = true

	updated, cmd, _ := handleKeyConfirmApply(mm)
	final := driveApplyToCompletion(t, updated, cmd)

	if final.applyResult == nil {
		t.Fatalf("expected a non-nil applyResult for a failed apply")
	}
	if final.applying {
		t.Fatalf("expected applying=false once the failed stream completes")
	}
}

func TestHandleKeyApplyRequiresConfirmation(t *testing.T) {
	m := NewModelWithApply(simplePlan(), "/dev/null", "true", "", "")
	m.applyMode = true

	updated, cmd, _ := handleKeyApply(m)
	if updated.applying || cmd != nil {
		t.Fatalf("first 'a' press should only arm confirmation, not start applying")
	}
	if !updated.confirmApply {
		t.Fatalf("expected confirmApply=true after the first 'a' press")
	}
}
