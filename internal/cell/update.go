package cell

import (
	"errors"
	"strings"
)

func (m *Manager) UpdateDefinition(
	id string,
	request UpdateCellDefinitionRequest,
) (*Cell, error) {
	id = strings.TrimSpace(id)
	request.Comb = strings.TrimSpace(request.Comb)

	if id == "" {
		return nil, errors.New("cell id is required")
	}

	if request.Comb == "" {
		return nil, errors.New("comb is required")
	}

	if request.CombData == nil {
		return nil, errors.New("comb data is required")
	}

	if request.Variables == nil {
		request.Variables = map[string]string{}
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	gameCell, exists := m.cells[id]
	if !exists {
		return nil, errors.New("cell not found")
	}

	if m.runtime.IsRunning(id) {
		return nil, errors.New("cell must be stopped before changing its definition")
	}

	gameCell.Comb = request.Comb
	gameCell.CombData = request.CombData
	gameCell.Variables = request.Variables

	if err := m.save(gameCell); err != nil {
		return nil, err
	}

	return gameCell, nil
}
