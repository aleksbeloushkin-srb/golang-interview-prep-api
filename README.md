# golang-interview-prep-api

Backend for [Go Interview Prep](https://aleksbeloushkin-srb.github.io/golang-interview-prep/):
Google sign-in, saved progress, and a runner that checks practice solutions with `go test -race`.

```
site (GitHub Pages) ──login──▶ Cognito (Google) ──access token──▶ HTTP API ──┬─▶ Lambda api (Go):  GET /me, GET|PUT /progress ─▶ DynamoDB
                                                                              └─▶ Lambda runner (Go image, no network): POST /runs
```

| Path | What |
|---|---|
| `cmd/api` | Lambda for `/me` and `/progress` (arm64, `provided.al2023`) |
| `cmd/runner` | Lambda for `/runs`: writes the code next to the task's tests and runs `go test -race -json` |
| `cmd/synctasks` | copies the tasks from the site's `data/practice.js` into `internal/tasks/data` |
| `internal/runner` | the runner itself: clean environment, time limit, process-group kill, `go test -json` parsing |
| `internal/store` | progress with optimistic locking: DynamoDB in production, memory in tests |
| `runner.Dockerfile`, `runner/warm` | runner image: Go 1.27.3 + gcc, build cache warmed for the tasks' packages |
| `infra` | CDK app (TypeScript): Cognito + Google, DynamoDB, HTTP API with JWT authorizer, both Lambdas |
| `.github/workflows` | `ci` on every push and PR, `deploy` on `main` |

## API

Every route needs `Authorization: Bearer <Cognito access token>`.

| Route | Body | Answer |
|---|---|---|
| `GET /me` | — | `{"sub", "username"}` |
| `GET /progress` | — | `{"progress": {...} or null, "version": n, "updatedAt"}` |
| `PUT /progress` | `{"version": n, "progress": {...}}` | `{"version": n+1, "updatedAt"}`; `409` if another device saved first |
| `POST /runs` | `{"taskId": "p-workerpool", "code": "package workerpool ..."}` | `{"passed", "tests": [{"name", "passed", "output"}], "build", "race", "timedOut", "output", "millis"}` |

## Local checks

```bash
go vet ./... && go test -race ./...
SITE_DIR=$PWD/../golang-interview-prep go test -run TestSiteSolutions -v ./internal/runner   # every task: solution passes, starter fails
cd infra && npm ci && npm run build && npm test && npx cdk synth -q
```

## Deploy

`deploy` runs on every push to `main`: tests, builds the API binary, then `cdk deploy`
(the runner image is built during deploy). AWS access: the repository secrets
`AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` of the IAM user `gh-deploy-prep`, which may only
assume the CDK bootstrap roles of account 851435021323 in `eu-north-1`. Google's client ID and
secret live only in Secrets Manager (`prep/google-oauth`).

## Security notes

- The runner Lambda sits in isolated subnets with a security group without rules: submitted code has no network.
- `go test` gets a fixed environment (no AWS credentials), a time limit, and is killed with its whole process group.
- The HTTP API is throttled to 5 requests per second (burst 10).
- Only Google test users of the OAuth app can sign in while it is in "Testing" mode.
