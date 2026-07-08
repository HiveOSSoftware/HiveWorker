package cell

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

type ConsoleSession struct {
	CellID    string
	ExpiresAt time.Time
}

func (m *Manager) CreateConsoleSession(cellID string) (string, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if _, exists := m.cells[cellID]; !exists {
		return "", errors.New("cell not found")
	}

	if m.consoleSessions == nil {
		m.consoleSessions = map[string]ConsoleSession{}
	}

	token := uuid.NewString()

	m.consoleSessions[token] = ConsoleSession{
		CellID:    cellID,
		ExpiresAt: time.Now().Add(30 * time.Second),
	}

	return token, nil
}

func (m *Manager) ValidateConsoleSession(cellID string, token string) bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.consoleSessions == nil {
		return false
	}

	session, exists := m.consoleSessions[token]
	if !exists {
		return false
	}

	delete(m.consoleSessions, token)

	if session.CellID != cellID {
		return false
	}

	if time.Now().After(session.ExpiresAt) {
		return false
	}

	return true
}
