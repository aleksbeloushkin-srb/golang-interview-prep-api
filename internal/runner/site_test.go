package runner_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aleksbeloushkin-srb/golang-interview-prep-api/internal/runner"
	"github.com/aleksbeloushkin-srb/golang-interview-prep-api/internal/tasks"
)

// TestSiteSolutions runs every embedded task against the site's reference
// solution (must pass) and starter (must fail). It needs a checkout of the
// site repository: SITE_DIR=../golang-interview-prep go test ./internal/runner
// Tasks with module requirements are skipped unless RUNNER_GOSUM points to a
// go.sum with those modules and they are in the module cache.
func TestSiteSolutions(t *testing.T) {
	site := os.Getenv("SITE_DIR")
	if site == "" {
		t.Skip("set SITE_DIR to a checkout of the site repository")
	}
	all, err := tasks.Load()
	if err != nil {
		t.Fatal(err)
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go command not found")
	}
	cache, _ := exec.Command(goBin, "env", "GOCACHE").Output()
	modCache, _ := exec.Command(goBin, "env", "GOMODCACHE").Output()
	r := &runner.Runner{
		GoBin:   goBin,
		TempDir: t.TempDir(),
		Env: []string{
			"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(),
			"GOCACHE=" + strings.TrimSpace(string(cache)),
			"GOMODCACHE=" + strings.TrimSpace(string(modCache)),
			"GOPROXY=off", "GOFLAGS=-mod=mod", "GOSUMDB=off", "GOTOOLCHAIN=local", "CGO_ENABLED=1",
		},
		Timeout: 90 * time.Second,
		Tasks:   all,
	}
	if path := os.Getenv("RUNNER_GOSUM"); path != "" {
		sum, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		r.GoSum = string(sum)
	}
	for id, task := range all {
		t.Run(id, func(t *testing.T) {
			if len(task.Requires) > 0 && r.GoSum == "" {
				t.Skip("needs RUNNER_GOSUM for " + strings.Join(keys(task.Requires), ", "))
			}
			dir := filepath.Join(site, "docs/verification/fixtures/practice", task.Package)
			if len(task.Requires) > 0 {
				dir = filepath.Join(site, "docs/verification/fixtures/practice-errgroup", task.Package)
			}
			solution, err := os.ReadFile(filepath.Join(dir, task.Package+".go"))
			if err != nil {
				t.Fatal(err)
			}
			res, err := r.Run(context.Background(), runner.Request{TaskID: id, Code: string(solution)})
			if err != nil {
				t.Fatal(err)
			}
			if !res.Passed {
				t.Fatalf("reference solution failed:\nbuild: %s\n%s", res.Build, res.Output)
			}
			starter, err := os.ReadFile(filepath.Join(dir, "starter.go.txt"))
			if err != nil {
				t.Fatal(err)
			}
			res, err = r.Run(context.Background(), runner.Request{TaskID: id, Code: string(starter)})
			if err != nil {
				t.Fatal(err)
			}
			if res.Passed {
				t.Fatal("the starter passed; its tests check nothing")
			}
		})
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
