// Package runner shells out to terraform/tofu to produce a JSON plan and
// to apply it. It is the only place in terraprism that invokes the
// terraform/tofu plan/show/apply commands.
package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/CaptShanks/terraprism/internal/tfplan"
)

// TFCommand is the executable to invoke: "terraform", "tofu", or (in
// tests) an absolute path to a stand-in script.
type TFCommand string

// Options controls a plan invocation.
type Options struct {
	Cmd  TFCommand
	Args []string // extra tf args (-target, -var, --, etc.), not including "plan"/"-out"/"-no-color"
	Dir  string   // working directory; "" = current directory

	// KeepPlanFile, when true, leaves the binary plan file on disk and
	// reports its path in PlanResult.PlanFile so the caller can later
	// run Apply against it. When false, the plan file is removed before
	// RunPlan returns.
	KeepPlanFile bool
}

// PlanResult is the outcome of a successful RunPlan.
type PlanResult struct {
	Plan     *tfplan.Plan
	PlanFile string // "" unless Options.KeepPlanFile was set
	RawJSON  []byte
}

// PlanError wraps a failed `plan` invocation, carrying the combined
// stdout+stderr output for display exactly as the CLI produced it.
type PlanError struct {
	Cmd    TFCommand
	Output []byte
	Err    error
}

func (e *PlanError) Error() string {
	return fmt.Sprintf("%s plan failed: %v", e.Cmd, e.Err)
}

func (e *PlanError) Unwrap() error { return e.Err }

// ShowError wraps a failed `show -json` invocation. Unlike a plan
// failure, this indicates a genuine bug or terraform/tofu incompatibility
// rather than an expected "plan had errors" case.
type ShowError struct {
	Cmd    TFCommand
	Stderr []byte
	Err    error
}

func (e *ShowError) Error() string {
	msg := fmt.Sprintf("%s show -json failed: %v", e.Cmd, e.Err)
	if len(e.Stderr) > 0 {
		msg += "\n" + string(e.Stderr)
	}
	return msg
}

func (e *ShowError) Unwrap() error { return e.Err }

// RunPlan runs `<cmd> plan -out=<tmpfile> -no-color <args...>`, then
// `<cmd> show -json <tmpfile>` and decodes the result via tfplan.Decode.
// A binary plan file is always written, since `show -json` requires one
// — there is no way to obtain JSON plan output without it.
func RunPlan(ctx context.Context, opts Options) (*PlanResult, error) {
	planFile := filepath.Join(os.TempDir(), fmt.Sprintf("terraprism-%d.tfplan", os.Getpid()))

	planArgs := append([]string{"plan", "-out=" + planFile, "-no-color"}, opts.Args...)
	planCmd := exec.CommandContext(ctx, string(opts.Cmd), planArgs...)
	planCmd.Dir = opts.Dir
	output, err := planCmd.CombinedOutput()
	if err != nil {
		os.Remove(planFile)
		return nil, &PlanError{Cmd: opts.Cmd, Output: output, Err: err}
	}

	showCmd := exec.CommandContext(ctx, string(opts.Cmd), "show", "-json", planFile)
	showCmd.Dir = opts.Dir
	jsonBytes, err := showCmd.Output()
	if err != nil {
		if !opts.KeepPlanFile {
			os.Remove(planFile)
		}
		var stderr []byte
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr = exitErr.Stderr
		}
		return nil, &ShowError{Cmd: opts.Cmd, Stderr: stderr, Err: err}
	}

	plan, err := tfplan.DecodeBytes(jsonBytes)
	if err != nil {
		if !opts.KeepPlanFile {
			os.Remove(planFile)
		}
		return nil, fmt.Errorf("decoding plan JSON: %w", err)
	}

	result := &PlanResult{Plan: plan, RawJSON: jsonBytes, PlanFile: planFile}
	if !opts.KeepPlanFile {
		os.Remove(planFile)
		result.PlanFile = ""
	}
	return result, nil
}

// Apply runs `<cmd> apply <planFile>`, streaming stdin/stdout/stderr
// directly so the user sees terraform/tofu's own apply progress output.
func Apply(ctx context.Context, cmd TFCommand, planFile string) error {
	applyCmd := exec.CommandContext(ctx, string(cmd), "apply", planFile)
	applyCmd.Stdout = os.Stdout
	applyCmd.Stderr = os.Stderr
	applyCmd.Stdin = os.Stdin
	return applyCmd.Run()
}

// DetectCommand returns "terraform" or "tofu" based on forceTofu and
// PATH availability, preferring terraform when neither is forced.
func DetectCommand(forceTofu bool) TFCommand {
	if forceTofu {
		return TFCommand("tofu")
	}
	if _, err := exec.LookPath("terraform"); err == nil {
		return TFCommand("terraform")
	}
	if _, err := exec.LookPath("tofu"); err == nil {
		return TFCommand("tofu")
	}
	return TFCommand("terraform")
}
