// Command synctasks copies the practice tasks from the site repository into
// internal/tasks/data: one JSON file per task with the package name and tests.
// The site's data/practice.js is the source of truth; run this after it changes:
//
//	go run ./cmd/synctasks -site ../golang-interview-prep
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aleksbeloushkin-srb/golang-interview-prep-api/internal/runner"
)

// practiceTask is the part of a site practice task the runner needs.
type practiceTask struct {
	ID    string   `json:"id"`
	Setup []string `json:"setup"`
	Files struct {
		Tests    struct{ Name, Code string } `json:"tests"`
		Solution struct{ Name string }       `json:"solution"`
	} `json:"files"`
}

var goGet = regexp.MustCompile(`go get ([^\s@]+)@(v\S+)`)

func main() {
	site := flag.String("site", "../golang-interview-prep", "path to the site repository")
	out := flag.String("out", "internal/tasks/data", "output directory")
	flag.Parse()

	src, err := os.ReadFile(filepath.Join(*site, "data", "practice.js"))
	if err != nil {
		log.Fatal(err)
	}
	// The file is "// comments\nconst PRACTICE = [ ...JSON... ];".
	start := bytes.Index(src, []byte("const PRACTICE = "))
	end := bytes.LastIndex(src, []byte("];"))
	if start < 0 || end < start {
		log.Fatal("data/practice.js: cannot find the PRACTICE array")
	}
	var tasks []practiceTask
	if err := json.Unmarshal(src[start+len("const PRACTICE = "):end+1], &tasks); err != nil {
		log.Fatalf("data/practice.js: %v", err)
	}

	if err := os.RemoveAll(*out); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}
	for _, p := range tasks {
		t := runner.Task{
			ID:       p.ID,
			Package:  strings.TrimSuffix(p.Files.Solution.Name, ".go"),
			TestFile: p.Files.Tests.Name,
			Tests:    p.Files.Tests.Code + "\n",
		}
		for _, line := range p.Setup {
			if m := goGet.FindStringSubmatch(line); m != nil {
				if t.Requires == nil {
					t.Requires = map[string]string{}
				}
				t.Requires[m[1]] = m[2]
			}
		}
		data, err := json.MarshalIndent(t, "", "  ")
		if err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(*out, p.ID+".json"), append(data, '\n'), 0o644); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Printf("synced %d tasks into %s\n", len(tasks), *out)
}
