// Package api serves the HTTP API behind API Gateway: GET /me and
// GET and PUT /progress. API Gateway has already checked the Cognito access
// token, so the handler only reads the user's ID from the token's claims.
package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/aws/aws-lambda-go/events"

	"github.com/aleksbeloushkin-srb/golang-interview-prep-api/internal/store"
)

// MaxProgressBytes limits a saved progress object (DynamoDB items stop at 400 KB).
const MaxProgressBytes = 300 << 10

// Handler handles API Gateway HTTP API (payload 2.0) requests.
type Handler struct {
	Store store.Store
	Log   *slog.Logger
}

type putRequest struct {
	Version  int64           `json:"version"`
	Progress json.RawMessage `json:"progress"`
}

func (h *Handler) Handle(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	claims := map[string]string{}
	if req.RequestContext.Authorizer != nil && req.RequestContext.Authorizer.JWT != nil {
		claims = req.RequestContext.Authorizer.JWT.Claims
	}
	userID := claims["sub"]
	if userID == "" {
		return reply(http.StatusUnauthorized, errorBody("no user in the token"))
	}

	switch req.RouteKey {
	case "GET /me":
		return reply(http.StatusOK, map[string]string{"sub": userID, "username": claims["username"]})

	case "GET /progress":
		p, ok, err := h.Store.GetProgress(ctx, userID)
		if err != nil {
			return h.internal(ctx, "get progress", err)
		}
		if !ok {
			return reply(http.StatusOK, store.Progress{Data: json.RawMessage("null")})
		}
		return reply(http.StatusOK, p)

	case "PUT /progress":
		body, err := requestBody(req)
		if err != nil || len(body) > MaxProgressBytes {
			return reply(http.StatusBadRequest, errorBody("body must be JSON up to 300 KB"))
		}
		var in putRequest
		if err := json.Unmarshal(body, &in); err != nil || len(in.Progress) == 0 || in.Progress[0] != '{' {
			return reply(http.StatusBadRequest, errorBody(`want {"version": n, "progress": {...}}`))
		}
		p, err := h.Store.PutProgress(ctx, userID, in.Progress, in.Version)
		if errors.Is(err, store.ErrConflict) {
			return reply(http.StatusConflict, errorBody("progress changed on another device; load it first"))
		}
		if err != nil {
			return h.internal(ctx, "put progress", err)
		}
		return reply(http.StatusOK, store.Progress{Version: p.Version, UpdatedAt: p.UpdatedAt, Data: json.RawMessage("null")})
	}
	return reply(http.StatusNotFound, errorBody("no such route"))
}

func (h *Handler) internal(ctx context.Context, op string, err error) (events.APIGatewayV2HTTPResponse, error) {
	if h.Log != nil {
		h.Log.ErrorContext(ctx, op, "err", err) // the details go to the log, not to the client
	}
	return reply(http.StatusInternalServerError, errorBody("internal error"))
}

func requestBody(req events.APIGatewayV2HTTPRequest) ([]byte, error) {
	if req.IsBase64Encoded {
		return base64.StdEncoding.DecodeString(req.Body)
	}
	return []byte(req.Body), nil
}

func errorBody(msg string) map[string]string { return map[string]string{"error": msg} }

// reply encodes v as the JSON body of a response with the given status.
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
