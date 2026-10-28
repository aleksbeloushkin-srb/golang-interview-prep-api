package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const sumTests = `package sum

import "testing"

func TestSum(t *testing.T) {
	if got := Sum(2, 3); got != 5 {
		t.Fatalf("Sum(2, 3) = %d, want 5", got)
	}
}

func TestZero(t *testing.T) {
	if got := Sum(0, 0); got != 0 {
		t.Fatalf("Sum(0, 0) = %d", got)
	}
}
`

const raceTests = `package counter

import (
	"sync"
	"testing"
)

func TestInc(t *testing.T) {
	var c Counter
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); for range 1000 { c.Inc() } }()
	}
	wg.Wait()
	_ = c.Value()
}
`

// testRunner uses the go command on PATH with a clean environment, the way
// the Lambda does, but with this machine's caches.
func testRunner(t *testing.T) *Runner {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go command not found")
	}
	cache, err := exec.Command(goBin, "env", "GOCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	return &Runner{
		GoBin:   goBin,
		TempDir: t.TempDir(),
		Env: []string{
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + t.TempDir(),
			"GOCACHE=" + strings.TrimSpace(string(cache)),
			"GOPATH=" + t.TempDir(),
			"GOPROXY=off",
			"GOFLAGS=-mod=mod",
			"GOTOOLCHAIN=local",
			"CGO_ENABLED=1",
		},
		Timeout: 60 * time.Second,
		Tasks: map[string]Task{
			"p-sum":     {ID: "p-sum", Package: "sum", TestFile: "sum_test.go", Tests: sumTests},
			"p-counter": {ID: "p-counter", Package: "counter", TestFile: "counter_test.go", Tests: raceTests},
		},
	}
}

func TestPassingSolution(t *testing.T) {
	r := testRunner(t)
	res, err := r.Run(context.Background(), Request{TaskID: "p-sum", Code: "package sum\n\nfunc Sum(a, b int) int { return a + b }\n"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed || len(res.Tests) != 2 || res.Build != "" || res.Race || res.TimedOut {
		t.Fatalf("got %+v, want two passing tests", res)
	}
}

func TestFailingTest(t *testing.T) {
	r := testRunner(t)
	res, err := r.Run(context.Background(), Request{TaskID: "p-sum", Code: "package sum\n\nfunc Sum(a, b int) int { return a - b }\n"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed {
		t.Fatal("a wrong solution passed")
	}
	var failed *TestResult
	for i := range res.Tests {
		if res.Tests[i].Name == "TestSum" {
			failed = &res.Tests[i]
		}
	}
	if failed == nil || failed.Passed || !strings.Contains(failed.Output, "want 5") {
		t.Fatalf("TestSum should fail with its message; got %+v", res.Tests)
	}
}

func TestCompileError(t *testing.T) {
	r := testRunner(t)
	res, err := r.Run(context.Background(), Request{TaskID: "p-sum", Code: "package sum\n\nfunc Sum(a, b int) int { return a + }\n"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed || res.Build == "" {
		t.Fatalf("want a build error, got %+v", res)
	}
}

func TestDataRaceIsReported(t *testing.T) {
	r := testRunner(t)
	code := "package counter\n\ntype Counter struct{ n int }\n\nfunc (c *Counter) Inc() { c.n++ }\nfunc (c *Counter) Value() int { return c.n }\n"
	res, err := r.Run(context.Background(), Request{TaskID: "p-counter", Code: code})
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed || !res.Race {
		t.Fatalf("want a reported data race, got passed=%v race=%v\n%s", res.Passed, res.Race, res.Output)
	}
}

func TestTimeoutKillsTheTest(t *testing.T) {
	r := testRunner(t)
	r.Timeout = 3 * time.Second
	start := time.Now()
	res, err := r.Run(context.Background(), Request{TaskID: "p-sum", Code: "package sum\n\nfunc Sum(a, b int) int { for {} }\n"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed || time.Since(start) > 10*time.Second {
		t.Fatalf("want a failed run within the limit, got %+v after %v", res, time.Since(start))
	}
}

func TestBadRequests(t *testing.T) {
	r := testRunner(t)
	for name, req := range map[string]Request{
		"unknown task":  {TaskID: "../etc", Code: "package x"},
		"empty code":    {TaskID: "p-sum", Code: "  "},
		"code too long": {TaskID: "p-sum", Code: "package sum\n" + strings.Repeat("/", MaxCodeBytes)},
	} {
		if _, err := r.Run(context.Background(), req); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%s: err = %v, want ErrBadRequest", name, err)
		}
	}
}

func TestEnvironmentIsNotInherited(t *testing.T) {
	r := testRunner(t)
	t.Setenv("AWS_SECRET_ACCESS_KEY", "must-not-leak")
	r.Tasks["p-env"] = Task{ID: "p-env", Package: "env", TestFile: "env_test.go", Tests: `package env

import (
	"os"
	"testing"
)

func TestNoSecrets(t *testing.T) {
	if v := os.Getenv("AWS_SECRET_ACCESS_KEY"); v != "" {
		t.Fatal("the test can read the runner's credentials")
	}
}
`}
	res, err := r.Run(context.Background(), Request{TaskID: "p-env", Code: "package env\n"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed {
		t.Fatalf("environment leaked into the test:\n%s", res.Output)
	}
}
