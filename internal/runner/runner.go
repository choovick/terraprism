// Package runner shells out to terraform/tofu to produce a JSON plan and
// to apply it. It is the only place in terraprism that invokes the
// terraform/tofu plan/show/apply commands.
package runner

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"

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
	Output   []byte // combined stdout+stderr of the `plan` invocation itself
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
//
// RunPlan is a thin synchronous wrapper over PlanStream (draining its
// line channel without exposing it), so a caller that doesn't need live
// progress gets identical behavior/output to one that does, by
// construction rather than by keeping two implementations in sync.
func RunPlan(ctx context.Context, opts Options) (*PlanResult, error) {
	lines, done := PlanStream(ctx, opts)
	for range lines {
	}
	res := <-done
	return res.Result, res.Err
}

// PlanLine is one line of streamed `plan` output.
type PlanLine struct {
	Text string
}

// PlanStreamResult is what PlanStream eventually delivers on its done
// channel: either a populated Result, or Err (a *PlanError, *ShowError,
// or plain decode error — see RunPlan's doc for what each means).
type PlanStreamResult struct {
	Result *PlanResult
	Err    error
}

// reserveTempPlanFile creates and immediately closes a uniquely-named
// temp file, returning its path for `plan -out=` to write into. Using
// os.CreateTemp's atomic O_EXCL creation (rather than a name derived
// only from os.Getpid, which the OS can reuse across processes) rules
// out a stale leftover from a killed/crashed process ever colliding
// with a later run.
func reserveTempPlanFile() (string, error) {
	f, err := os.CreateTemp("", "terraprism-*.tfplan")
	if err != nil {
		return "", err
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		return "", err
	}
	return path, nil
}

// PlanStream starts `<cmd> plan -out=<tmpfile> -no-color <args...>`,
// merging stdout and stderr into a single ordered stream of lines the
// same way ApplyStream does, and returns immediately once the
// subprocess has started. Lines arrive on the returned channel as
// they're produced; lines is always fully drained and closed before
// exactly one PlanStreamResult is sent on done, after which done is
// closed too.
//
// If `plan` itself succeeds, PlanStream goes on to run `<cmd> show
// -json` (fast and not itself streamed — it's a single JSON blob, not
// line-oriented progress) and decode it before signaling done, so a
// successful PlanStreamResult always carries a fully-decoded Plan ready
// to display; a caller never sees "done" without either a usable Plan or
// an error explaining why not.
func PlanStream(ctx context.Context, opts Options) (<-chan PlanLine, <-chan PlanStreamResult) {
	lines := make(chan PlanLine, 64)
	done := make(chan PlanStreamResult, 1)

	planFile, err := reserveTempPlanFile()
	if err != nil {
		close(lines)
		done <- PlanStreamResult{Err: &PlanError{Cmd: opts.Cmd, Err: fmt.Errorf("creating temp plan file: %w", err)}}
		close(done)
		return lines, done
	}
	planArgs := append([]string{"plan", "-out=" + planFile, "-no-color"}, opts.Args...)
	planCmd := exec.CommandContext(ctx, string(opts.Cmd), planArgs...)
	planCmd.Dir = opts.Dir

	pr, pw := io.Pipe()
	planCmd.Stdout = pw
	planCmd.Stderr = pw

	if err := planCmd.Start(); err != nil {
		_ = pw.Close()
		close(lines)
		done <- PlanStreamResult{Err: &PlanError{Cmd: opts.Cmd, Err: err}}
		close(done)
		return lines, done
	}

	waitErr := make(chan error, 1)
	go func() {
		waitErr <- planCmd.Wait()
		_ = pw.Close()
	}()

	go func() {
		var output bytes.Buffer
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			text := scanner.Text()
			output.WriteString(text)
			output.WriteByte('\n')
			lines <- PlanLine{Text: text}
		}
		close(lines)

		if err := <-waitErr; err != nil {
			_ = os.Remove(planFile)
			done <- PlanStreamResult{Err: &PlanError{Cmd: opts.Cmd, Output: output.Bytes(), Err: err}}
			close(done)
			return
		}

		showCmd := exec.CommandContext(ctx, string(opts.Cmd), "show", "-json", planFile)
		showCmd.Dir = opts.Dir
		jsonBytes, err := showCmd.Output()
		if err != nil {
			if !opts.KeepPlanFile {
				_ = os.Remove(planFile)
			}
			var stderr []byte
			if exitErr, ok := err.(*exec.ExitError); ok {
				stderr = exitErr.Stderr
			}
			done <- PlanStreamResult{Err: &ShowError{Cmd: opts.Cmd, Stderr: stderr, Err: err}}
			close(done)
			return
		}

		plan, err := tfplan.DecodeBytes(jsonBytes)
		if err != nil {
			if !opts.KeepPlanFile {
				_ = os.Remove(planFile)
			}
			done <- PlanStreamResult{Err: fmt.Errorf("decoding plan JSON: %w", err)}
			close(done)
			return
		}

		result := &PlanResult{Plan: plan, RawJSON: jsonBytes, PlanFile: planFile, Output: output.Bytes()}
		if !opts.KeepPlanFile {
			_ = os.Remove(planFile)
			result.PlanFile = ""
		}
		done <- PlanStreamResult{Result: result}
		close(done)
	}()

	return lines, done
}

// ApplyLine is one line of streamed `apply` output.
type ApplyLine struct {
	Text string
}

// ApplyStream starts `<cmd> apply -auto-approve <planFile>`, merging
// stdout and stderr into a single ordered stream of lines the way a real
// terminal would (one process, one pipe, genuine OS-level interleaving),
// and returns immediately once the subprocess has started. Lines arrive
// on the returned channel as they're produced; exactly one value (nil on
// success) is sent on the done channel once the process exits, after
// which both channels are closed. lines is always fully drained and
// closed before done fires, so a caller doing `for range lines` and then
// reading done never misses trailing output.
//
// -auto-approve is required: unlike Apply, ApplyStream does not connect
// the subprocess's stdin to the terminal (the caller, e.g. a running
// TUI, owns the terminal instead), so terraform/tofu's own interactive
// approval prompt cannot work here — the caller is expected to have
// already gotten the user's confirmation before calling this.
//
// internal/runner takes no UI-framework dependency: pumping lines/done
// into an event loop (e.g. a Bubble Tea tea.Cmd) is the caller's job.
func ApplyStream(ctx context.Context, cmd TFCommand, planFile string) (<-chan ApplyLine, <-chan error) {
	lines := make(chan ApplyLine, 64)
	done := make(chan error, 1)

	applyCmd := exec.CommandContext(ctx, string(cmd), "apply", "-auto-approve", planFile)
	pr, pw := io.Pipe()
	applyCmd.Stdout = pw
	applyCmd.Stderr = pw

	if err := applyCmd.Start(); err != nil {
		_ = pw.Close()
		close(lines)
		done <- err
		close(done)
		return lines, done
	}

	waitErr := make(chan error, 1)
	go func() {
		waitErr <- applyCmd.Wait()
		_ = pw.Close()
	}()

	go func() {
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			lines <- ApplyLine{Text: scanner.Text()}
		}
		close(lines)
		done <- <-waitErr
		close(done)
	}()

	return lines, done
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
