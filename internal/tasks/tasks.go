// Package tasks holds the practice tasks the runner knows, generated from the
// site by cmd/synctasks and embedded into the binary.
package tasks

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"

	"github.com/aleksbeloushkin-srb/golang-interview-prep-api/internal/runner"
)

//go:embed data/*.json
var files embed.FS

// Load returns every embedded task by ID.
func Load() (map[string]runner.Task, error) {
	names, err := fs.Glob(files, "data/*.json")
	if err != nil {
		return nil, err
	}
	tasks := make(map[string]runner.Task, len(names))
	for _, name := range names {
		data, err := files.ReadFile(name)
		if err != nil {
			return nil, err
		}
		var t runner.Task
		if err := json.Unmarshal(data, &t); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if t.ID == "" || t.Package == "" || t.TestFile == "" || t.Tests == "" {
			return nil, fmt.Errorf("%s: incomplete task", name)
		}
		tasks[t.ID] = t
	}
	return tasks, nil
}
