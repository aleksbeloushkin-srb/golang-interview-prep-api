# Runner image: the Lambda bootstrap plus a Go toolchain and gcc, because
# go test -race needs cgo. Built by CDK during deploy (linux/amd64).
ARG GO_VERSION=1.27.3

FROM golang:${GO_VERSION} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags lambda.norpc -trimpath -o /out/bootstrap ./cmd/runner

FROM golang:${GO_VERSION} AS toolchain

FROM public.ecr.aws/lambda/provided:al2023
RUN dnf install -y gcc && dnf clean all
COPY --from=toolchain /usr/local/go /usr/local/go
ENV PATH=/usr/local/go/bin:$PATH \
    GOTOOLCHAIN=local \
    GOTELEMETRY=off
# Download x/sync and build the race-instrumented packages the tasks use into
# read-only caches; each cold start copies the build cache to /tmp.
COPY runner/warm /opt/warm
RUN cd /opt/warm \
 && GOCACHE=/opt/gocache GOMODCACHE=/opt/gomod go mod tidy \
 && GOCACHE=/opt/gocache GOMODCACHE=/opt/gomod CGO_ENABLED=1 go test -race -count=1 ./... \
 && chmod -R a+rX /opt/gocache /opt/gomod /opt/warm
COPY --from=build /out/bootstrap ${LAMBDA_RUNTIME_DIR}/bootstrap
CMD ["runner"]
