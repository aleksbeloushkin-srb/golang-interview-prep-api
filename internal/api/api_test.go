package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"

	"github.com/aleksbeloushkin-srb/golang-interview-prep-api/internal/store"
)

func request(route, sub, body string) events.APIGatewayV2HTTPRequest {
	req := events.APIGatewayV2HTTPRequest{RouteKey: route, Body: body}
	if sub != "" {
		req.RequestContext.Authorizer = &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{
			JWT: &events.APIGatewayV2HTTPRequestContextAuthorizerJWTDescription{
				Claims: map[string]string{"sub": sub, "username": "google_123", "token_use": "access"},
			},
		}
	}
	return req
}

func call(t *testing.T, h *Handler, req events.APIGatewayV2HTTPRequest) (int, map[string]any) {
	t.Helper()
	resp, err := h.Handle(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(resp.Body), &body); err != nil {
		t.Fatalf("body is not JSON: %q", resp.Body)
	}
	return resp.StatusCode, body
}

func TestMe(t *testing.T) {
	h := &Handler{Store: &store.Memory{}}
	code, body := call(t, h, request("GET /me", "user-1", ""))
	if code != http.StatusOK || body["sub"] != "user-1" || body["username"] != "google_123" {
		t.Fatalf("got %d %v", code, body)
	}
}

func TestNoTokenClaims(t *testing.T) {
	h := &Handler{Store: &store.Memory{}}
	if code, _ := call(t, h, request("GET /me", "", "")); code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", code)
	}
}

func TestProgressRoundTrip(t *testing.T) {
	h := &Handler{Store: &store.Memory{}}
	code, body := call(t, h, request("GET /progress", "u", ""))
	if code != http.StatusOK || body["progress"] != nil || body["version"] != 0.0 {
		t.Fatalf("empty progress: got %d %v", code, body)
	}
	code, body = call(t, h, request("PUT /progress", "u", `{"version":0,"progress":{"c1":"known"}}`))
	if code != http.StatusOK || body["version"] != 1.0 {
		t.Fatalf("first save: got %d %v", code, body)
	}
	code, body = call(t, h, request("GET /progress", "u", ""))
	if p, _ := body["progress"].(map[string]any); code != http.StatusOK || p["c1"] != "known" || body["version"] != 1.0 {
		t.Fatalf("load: got %d %v", code, body)
	}
	// Another user sees nothing.
	if _, body := call(t, h, request("GET /progress", "other", "")); body["progress"] != nil {
		t.Fatalf("progress leaked to another user: %v", body)
	}
}

func TestStaleVersionConflicts(t *testing.T) {
	h := &Handler{Store: &store.Memory{}}
	call(t, h, request("PUT /progress", "u", `{"version":0,"progress":{"a":1}}`))
	if code, _ := call(t, h, request("PUT /progress", "u", `{"version":0,"progress":{"a":2}}`)); code != http.StatusConflict {
		t.Fatalf("got %d, want 409 for a stale version", code)
	}
}

func TestBadBodies(t *testing.T) {
	h := &Handler{Store: &store.Memory{}}
	for _, body := range []string{"", "not json", `{"version":0}`, `{"version":0,"progress":[1,2]}`, `{"version":0,"progress":{"x":"` + strings.Repeat("a", MaxProgressBytes) + `"}}`} {
		if code, _ := call(t, h, request("PUT /progress", "u", body)); code != http.StatusBadRequest {
			t.Errorf("body %.40q: got %d, want 400", body, code)
		}
	}
}

func TestUnknownRoute(t *testing.T) {
	h := &Handler{Store: &store.Memory{}}
	if code, _ := call(t, h, request("DELETE /progress", "u", "")); code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", code)
	}
}
