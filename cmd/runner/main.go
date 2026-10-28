// Command runner is the Lambda that checks practice solutions: POST /runs.
// It runs in the container image built from runner.Dockerfile.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"

	"github.com/aleksbeloushkin-srb/golang-interview-prep-api/internal/runner"
	"github.com/aleksbeloushkin-srb/golang-interview-prep-api/internal/tasks"
)

const (
	imageCache = "/opt/gocache" // build cache warmed in the image (read-only)
	runCache   = "/tmp/gocache" // the go command needs a writable cache
	goSumPath  = "/opt/warm/go.sum"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	r, err := newRunner()
	if err != nil {
		log.Error("init", "err", err)
		os.Exit(1)
	}
	lambda.Start(func(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		return handle(ctx, r, log, req)
	})
}

func newRunner() (*runner.Runner, error) {
	all, err := tasks.Load()
	if err != nil {
		return nil, err
	}
	// Copy the warm build cache once per cold start: /opt is read-only.
	if _, err := os.Stat(runCache); errors.Is(err, os.ErrNotExist) {
		if out, err := exec.Command("cp", "-a", imageCache, runCache).CombinedOutput(); err != nil {
			return nil, errors.New("copy build cache: " + string(out))
		}
	}
	goSum, err := os.ReadFile(goSumPath)
	if err != nil {
		return nil, err
	}
	timeout, err := time.ParseDuration(os.Getenv("RUN_TIMEOUT"))
	if err != nil {
		timeout = 25 * time.Second
	}
	if err := os.MkdirAll("/tmp/runs", 0o755); err != nil {
		return nil, err
	}
	return &runner.Runner{
		GoBin:   "/usr/local/go/bin/go",
		TempDir: "/tmp/runs",
		// The whole environment of go test: no AWS credentials, no network
		// module downloads, caches in /tmp and the image.
		Env: []string{
			"PATH=/usr/local/go/bin:/usr/bin:/bin",
			"HOME=/tmp",
			"TMPDIR=/tmp",
			"GOCACHE=" + runCache,
			"GOMODCACHE=/opt/gomod",
			"GOPATH=/tmp/gopath",
			"GOPROXY=off",
			"GOSUMDB=off",
			"GOFLAGS=-mod=mod",
			"GOTOOLCHAIN=local",
			"GOTELEMETRY=off",
			"CGO_ENABLED=1",
		},
		GoSum:   string(goSum),
		Timeout: timeout,
		Tasks:   all,
	}, nil
}

func handle(ctx context.Context, r *runner.Runner, log *slog.Logger, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	// API Gateway's JWT authorizer already rejects requests without a valid
	// Cognito token. This second check means the runner never runs code for a
	// request without a user, even if the route were misconfigured.
	sub := userID(req)
	if sub == "" {
		return reply(http.StatusUnauthorized, map[string]string{"error": "sign in first"})
	}
	body := []byte(req.Body)
	if req.IsBase64Encoded {
		var err error
		if body, err = base64.StdEncoding.DecodeString(req.Body); err != nil {
			return reply(http.StatusBadRequest, map[string]string{"error": "bad body encoding"})
		}
	}
	var in runner.Request
	if err := json.Unmarshal(body, &in); err != nil {
		return reply(http.StatusBadRequest, map[string]string{"error": `want {"taskId": "...", "code": "..."}`})
	}
	res, err := r.Run(ctx, in)
	if errors.Is(err, runner.ErrBadRequest) {
		return reply(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if err != nil {
		log.ErrorContext(ctx, "run", "task", in.TaskID, "err", err)
		return reply(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
	log.InfoContext(ctx, "run", "task", in.TaskID, "user", sub, "passed", res.Passed, "ms", res.Millis, "timedOut", res.TimedOut)
	return reply(http.StatusOK, res)
}

// userID is the Cognito user from the validated access token, or "".
func userID(req events.APIGatewayV2HTTPRequest) string {
	a := req.RequestContext.Authorizer
	if a == nil || a.JWT == nil || a.JWT.Claims["token_use"] != "access" {
		return ""
	}
	return a.JWT.Claims["sub"]
}

func reply(status int, v any) (events.APIGatewayV2HTTPResponse, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return events.APIGatewayV2HTTPResponse{}, err
	}
	return events.APIGatewayV2HTTPResponse{
		StatusCode: status,
		Headers:    map[string]string{"content-type": "application/json", "cache-control": "no-store"},
		Body:       string(body),
	}, nil
}
