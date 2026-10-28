// Package store keeps per-user data. The API depends on the Store interface;
// DynamoDB implements it in production and Memory in tests.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// ErrConflict means the progress changed since the version the client saw.
var ErrConflict = errors.New("store: version conflict")

// Progress is the site's saved state for one user, as an opaque JSON object.
type Progress struct {
	Data      json.RawMessage `json:"progress"`
	Version   int64           `json:"version"`
	UpdatedAt time.Time       `json:"updatedAt,omitzero"`
}

// Store reads and writes progress.
type Store interface {
	// GetProgress returns ok == false if the user has saved nothing yet.
	GetProgress(ctx context.Context, userID string) (p Progress, ok bool, err error)
	// PutProgress saves data if the stored version still equals version
	// (0 for "nothing saved yet") and returns the new state; otherwise ErrConflict.
	PutProgress(ctx context.Context, userID string, data json.RawMessage, version int64) (Progress, error)
}

// Memory is an in-memory Store for tests and local runs.
type Memory struct {
	mu   sync.Mutex
	data map[string]Progress
	Now  func() time.Time
}

func (m *Memory) GetProgress(_ context.Context, userID string) (Progress, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.data[userID]
	return p, ok, nil
}

func (m *Memory) PutProgress(_ context.Context, userID string, data json.RawMessage, version int64) (Progress, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		m.data = map[string]Progress{}
	}
	if m.data[userID].Version != version {
		return Progress{}, ErrConflict
	}
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	p := Progress{Data: append(json.RawMessage(nil), data...), Version: version + 1, UpdatedAt: now().UTC()}
	m.data[userID] = p
	return p, nil
}
