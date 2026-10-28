// Package runner checks a practice solution: it writes the user's code next to
// the task's tests in a fresh module and runs go test -race -json.
package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// MaxCodeBytes limits the size of a submitted solution.
const MaxCodeBytes = 64 << 10

// maxOutputBytes limits how much go test output is kept and returned.
const maxOutputBytes = 256 << 10

// Task is one practice task: the package name and the tests the solution must pass.
type Task struct {
	ID       string            `json:"id"`
	Package  string            `json:"package"`
	TestFile string            `json:"testFile"`
	Tests    string            `json:"tests"`
	Requires map[string]string `json:"requires,omitempty"` // module path → version
}

// Request is what the site sends.
type Request struct {
	TaskID string `json:"taskId"`
	Code   string `json:"code"`
}

// TestResult is the outcome of one test or subtest.
type TestResult struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Output string `json:"output,omitempty"` // only for failures
}

// Result is what the site gets back.
type Result struct {
	TaskID   string       `json:"taskId"`
	Passed   bool         `json:"passed"`
	Tests    []TestResult `json:"tests"`
	Build    string       `json:"build,omitempty"` // compiler errors
	Race     bool         `json:"race"`            // the race detector reported a data race
	TimedOut bool         `json:"timedOut"`
	Output   string       `json:"output"`
	Millis   int64        `json:"millis"`
}

// ErrBadRequest marks problems with the request itself.
var ErrBadRequest = errors.New("bad request")

// Runner runs go test in a sandbox directory with a fixed environment.
type Runner struct {
	GoBin   string          // path to the go command
	TempDir string          // parent directory for per-run modules
	Env     []string        // the whole environment of go test; nothing else is inherited
	GoSum   string          // go.sum lines for modules in Task.Requires
	Timeout time.Duration   // wall-clock limit for one run
	Tasks   map[string]Task // known tasks by ID
}

// Run checks req and returns the test results. Errors are only for problems
// with the request or the runner itself; failing tests are a normal Result.
func (r *Runner) Run(ctx context.Context, req Request) (Result, error) {
	task, ok := r.Tasks[req.TaskID]
	if !ok {
		return Result{}, fmt.Errorf("%w: unknown task %q", ErrBadRequest, req.TaskID)
	}
	if strings.TrimSpace(req.Code) == "" || len(req.Code) > MaxCodeBytes {
		return Result{}, fmt.Errorf("%w: code must be 1..%d bytes", ErrBadRequest, MaxCodeBytes)
	}

	dir, err := os.MkdirTemp(r.TempDir, "run-")
	if err != nil {
		return Result{}, fmt.Errorf("create run dir: %w", err)
	}
	defer os.RemoveAll(dir)
	if err := writeModule(dir, task, req.Code, r.GoSum); err != nil {
		return Result{}, err
	}

	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// -timeout is the test binary's own limit; the context is the hard stop for
	// everything, including compilation.
	cmd := exec.CommandContext(ctx, r.GoBin, "test", "-race", "-count=1", "-json",
		"-timeout="+(timeout-time.Second).String(), "./...")
	cmd.Dir = dir
	cmd.Env = r.Env
	// Kill the whole process group on timeout: go test starts the test binary
	// as a child, and killing only the go command would leave it running.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	var stdout, stderr limitedBuffer
	stdout.limit, stderr.limit = maxOutputBytes, maxOutputBytes
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	start := time.Now()
	runErr := cmd.Run()
	res := parse(stdout.Bytes())
	res.TaskID = task.ID
	res.Millis = time.Since(start).Milliseconds()
	res.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
	if s := strings.TrimSpace(stderr.String()); s != "" {
		res.Build = strings.TrimSpace(res.Build + "\n" + s)
	}
	res.Race = strings.Contains(res.Output, "WARNING: DATA RACE")
	res.Passed = runErr == nil && !res.TimedOut && len(res.Tests) > 0 && allPassed(res.Tests)
	var exitErr *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exitErr) && !res.TimedOut {
		return Result{}, fmt.Errorf("run go test: %w", runErr)
	}
	return res, nil
}

func writeModule(dir string, task Task, code, goSum string) error {
	var mod strings.Builder
	fmt.Fprintf(&mod, "module example.com/%s\n\ngo 1.24\n", task.Package)
	for path, version := range task.Requires {
		fmt.Fprintf(&mod, "\nrequire %s %s\n", path, version)
	}
	files := map[string]string{
		"go.mod":             mod.String(),
		task.Package + ".go": code,
		task.TestFile:        task.Tests,
	}
	if len(task.Requires) > 0 {
		files["go.sum"] = goSum
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return nil
}

// event is one line of go test -json (test2json, plus build events since Go 1.24).
type event struct {
	Action string
	Test   string
	Output string
}

// parse turns go test -json output into a Result. Lines that are not JSON
// (for example from a crashed binary) are kept in Output.
func parse(out []byte) Result {
	var res Result
	var all, build strings.Builder
	perTest := map[string]*strings.Builder{}
	index := map[string]int{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), maxOutputBytes)
	for sc.Scan() {
		var e event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			all.Write(sc.Bytes())
			all.WriteByte('\n')
			continue
		}
		switch e.Action {
		case "build-output":
			build.WriteString(e.Output)
		case "output":
			all.WriteString(e.Output)
			if e.Test != "" {
				b := perTest[e.Test]
				if b == nil {
					b = &strings.Builder{}
					perTest[e.Test] = b
				}
				b.WriteString(e.Output)
			}
		case "pass", "fail", "skip":
			if e.Test == "" {
				continue
			}
			tr := TestResult{Name: e.Test, Passed: e.Action != "fail"}
			if !tr.Passed && perTest[e.Test] != nil {
				tr.Output = perTest[e.Test].String()
			}
			if i, ok := index[e.Test]; ok {
				res.Tests[i] = tr
			} else {
				index[e.Test] = len(res.Tests)
				res.Tests = append(res.Tests, tr)
			}
		}
	}
	res.Output = all.String()
	res.Build = strings.TrimSpace(build.String())
	return res
}

func allPassed(tests []TestResult) bool {
	for _, t := range tests {
		if !t.Passed {
			return false
		}
	}
	return true
}

// limitedBuffer keeps the first limit bytes and drops the rest.
type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.Len(); room > 0 {
		if len(p) > room {
			b.Buffer.Write(p[:room])
		} else {
			b.Buffer.Write(p)
		}
	}
	return len(p), nil
}
