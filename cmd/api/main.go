// Command api is the Lambda behind GET /me and GET and PUT /progress.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/aleksbeloushkin-srb/golang-interview-prep-api/internal/api"
	"github.com/aleksbeloushkin-srb/golang-interview-prep-api/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Error("aws config", "err", err)
		os.Exit(1)
	}
	h := &api.Handler{
		Store: &store.Dynamo{Client: dynamodb.NewFromConfig(cfg), Table: os.Getenv("TABLE_NAME")},
		Log:   log,
	}
	lambda.Start(h.Handle)
}
