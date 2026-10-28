package tasks

import "testing"

func TestLoad(t *testing.T) {
	tasks, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) < 18 {
		t.Fatalf("loaded %d tasks, want at least 18", len(tasks))
	}
	pf, ok := tasks["p-parallelfetch"]
	if !ok || pf.Requires["golang.org/x/sync"] == "" {
		t.Fatalf("p-parallelfetch should require golang.org/x/sync: %+v", pf.Requires)
	}
	if w := tasks["p-workerpool"]; w.Package != "workerpool" || w.TestFile != "workerpool_test.go" {
		t.Fatalf("p-workerpool = %+v", w)
	}
}
