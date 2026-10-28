package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

// Without a user from the authorizer the runner refuses before touching the
// code: r is nil, so any attempt to run would panic.
func TestRefusesRequestsWithoutAUser(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	body := `{"taskId":"p-money","code":"package money"}`
	for name, req := range map[string]events.APIGatewayV2HTTPRequest{
		"no authorizer": {Body: body},
		"no claims": {Body: body, RequestContext: events.APIGatewayV2HTTPRequestContext{
			Authorizer: &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{JWT: &events.APIGatewayV2HTTPRequestContextAuthorizerJWTDescription{}},
		}},
		"id token instead of access token": {Body: body, RequestContext: events.APIGatewayV2HTTPRequestContext{
			Authorizer: &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{JWT: &events.APIGatewayV2HTTPRequestContextAuthorizerJWTDescription{
				Claims: map[string]string{"sub": "u", "token_use": "id"},
			}},
		}},
	} {
		resp, err := handle(context.Background(), nil, log, req)
		if err != nil || resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: got %d, %v; want 401", name, resp.StatusCode, err)
		}
	}
}
