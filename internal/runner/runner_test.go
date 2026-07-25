package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const fakeTFScript = `#!/bin/sh
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
    printf '%s' "$FAKE_TF_PLAN_STDOUT"
    exit "${FAKE_TF_PLAN_EXIT:-0}"
    ;;
  show)
    printf '%s' "$FAKE_TF_SHOW_STDOUT"
    printf '%s' "$FAKE_TF_SHOW_STDERR" >&2
    exit "${FAKE_TF_SHOW_EXIT:-0}"
    ;;
  apply)
    exit "${FAKE_TF_APPLY_EXIT:-0}"
    ;;
esac
exit 1
`

const validPlanJSON = `{"format_version":"1.2","terraform_version":"1.7.5","resource_changes":[{"address":"null_resource.x","mode":"managed","type":"null_resource","name":"x","change":{"actions":["create"],"before":null,"after":{}}}],"output_changes":{}}`

func writeFakeTF(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "faketf")
	if err := os.WriteFile(path, []byte(fakeTFScript), 0o755); err != nil {
		t.Fatalf("writing fake tf script: %v", err)
	}
	return path
}

func TestRunPlanSuccess(t *testing.T) {
	t.Setenv("FAKE_TF_SHOW_STDOUT", validPlanJSON)
	fake := writeFakeTF(t)

	result, err := RunPlan(context.Background(), Options{Cmd: TFCommand(fake)})
	if err != nil {
		t.Fatalf("RunPlan: %v", err)
	}
	if result.Plan == nil || len(result.Plan.Resources) != 1 {
		t.Fatalf("expected 1 decoded resource, got %+v", result.Plan)
	}
	if result.PlanFile != "" {
		t.Errorf("PlanFile should be empty when KeepPlanFile is false, got %q", result.PlanFile)
	}
	if len(result.RawJSON) == 0 {
		t.Errorf("RawJSON should not be empty")
	}
}

func TestRunPlanKeepsPlanFile(t *testing.T) {
	t.Setenv("FAKE_TF_SHOW_STDOUT", validPlanJSON)
	fake := writeFakeTF(t)

	result, err := RunPlan(context.Background(), Options{Cmd: TFCommand(fake), KeepPlanFile: true})
	if err != nil {
		t.Fatalf("RunPlan: %v", err)
	}
	if result.PlanFile == "" {
		t.Fatalf("expected PlanFile to be set when KeepPlanFile is true")
	}
	defer os.Remove(result.PlanFile)
	if _, err := os.Stat(result.PlanFile); err != nil {
		t.Errorf("plan file should exist on disk: %v", err)
	}
}

func TestRunPlanFailure(t *testing.T) {
	t.Setenv("FAKE_TF_PLAN_EXIT", "1")
	t.Setenv("FAKE_TF_PLAN_STDOUT", "Error: something went wrong\n")
	fake := writeFakeTF(t)

	_, err := RunPlan(context.Background(), Options{Cmd: TFCommand(fake)})
	if err == nil {
		t.Fatalf("expected error")
	}
	var planErr *PlanError
	if !errors.As(err, &planErr) {
		t.Fatalf("expected *PlanError, got %T: %v", err, err)
	}
	if string(planErr.Output) != "Error: something went wrong\n" {
		t.Errorf("Output = %q", planErr.Output)
	}
}

func TestRunPlanShowFailure(t *testing.T) {
	t.Setenv("FAKE_TF_SHOW_EXIT", "1")
	t.Setenv("FAKE_TF_SHOW_STDERR", "Error: show failed\n")
	fake := writeFakeTF(t)

	_, err := RunPlan(context.Background(), Options{Cmd: TFCommand(fake)})
	if err == nil {
		t.Fatalf("expected error")
	}
	var showErr *ShowError
	if !errors.As(err, &showErr) {
		t.Fatalf("expected *ShowError, got %T: %v", err, err)
	}
	if string(showErr.Stderr) != "Error: show failed\n" {
		t.Errorf("Stderr = %q", showErr.Stderr)
	}
}

func TestRunPlanInvalidJSON(t *testing.T) {
	t.Setenv("FAKE_TF_SHOW_STDOUT", "not json")
	fake := writeFakeTF(t)

	_, err := RunPlan(context.Background(), Options{Cmd: TFCommand(fake)})
	if err == nil {
		t.Fatalf("expected decode error")
	}
}

func TestApply(t *testing.T) {
	tests := []struct {
		name    string
		exit    string
		wantErr bool
	}{
		{name: "success", exit: "0", wantErr: false},
		{name: "failure", exit: "1", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FAKE_TF_APPLY_EXIT", tc.exit)
			fake := writeFakeTF(t)
			err := Apply(context.Background(), TFCommand(fake), "/dev/null")
			if tc.wantErr && err == nil {
				t.Errorf("expected error")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestDetectCommand(t *testing.T) {
	t.Run("forceTofu always wins", func(t *testing.T) {
		if got := DetectCommand(true); got != "tofu" {
			t.Errorf("DetectCommand(true) = %q, want tofu", got)
		}
	})

	t.Run("prefers terraform when both present", func(t *testing.T) {
		dir := t.TempDir()
		writeStub(t, dir, "terraform")
		writeStub(t, dir, "tofu")
		t.Setenv("PATH", dir)
		if got := DetectCommand(false); got != "terraform" {
			t.Errorf("DetectCommand(false) = %q, want terraform", got)
		}
	})

	t.Run("falls back to tofu when terraform absent", func(t *testing.T) {
		dir := t.TempDir()
		writeStub(t, dir, "tofu")
		t.Setenv("PATH", dir)
		if got := DetectCommand(false); got != "tofu" {
			t.Errorf("DetectCommand(false) = %q, want tofu", got)
		}
	})

	t.Run("defaults to terraform when neither present", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("PATH", dir)
		if got := DetectCommand(false); got != "terraform" {
			t.Errorf("DetectCommand(false) = %q, want terraform", got)
		}
	})
}

func writeStub(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("writing stub %s: %v", name, err)
	}
}
