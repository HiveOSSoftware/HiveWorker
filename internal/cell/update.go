package cell

import (
	"errors"
	"strings"

	"hivepanel-worker/internal/allocation"
)

func (m *Manager) UpdateDefinition(id string, request UpdateCellDefinitionRequest) (*Cell, error) {
	id = strings.TrimSpace(id)
	request.Name = strings.TrimSpace(request.Name)
	request.Comb = strings.TrimSpace(request.Comb)

	if id == "" {
		return nil, errors.New("cell id is required")
	}

	if request.Name == "" {
		return nil, errors.New("cell name is required")
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

	request.Allocation.IP = strings.TrimSpace(request.Allocation.IP)

	if request.Allocation.IP == "" || request.Allocation.Port <= 0 {
		return nil, errors.New("allocation is required")
	}

	additionalAllocations, err := normaliseAdditionalAllocations(
		request.Allocation,
		request.AdditionalAllocations,
	)
	if err != nil {
		return nil, err
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

	oldAllocations := cellAllocations(gameCell)

	newAllocations := make([]allocation.Allocation, 0, 1+len(additionalAllocations))
	newAllocations = append(newAllocations, request.Allocation)
	newAllocations = append(newAllocations, additionalAllocations...)

	m.allocManager.ReplaceReservations(
		oldAllocations,
		newAllocations,
	)

	gameCell.Name = request.Name
	gameCell.Comb = request.Comb
	gameCell.CombData = request.CombData
	gameCell.Variables = request.Variables
	gameCell.Allocation = request.Allocation
	gameCell.AdditionalAllocations = additionalAllocations
	gameCell.Limits = request.Limits

	if err := m.save(gameCell); err != nil {
		m.allocManager.ReplaceReservations(
			newAllocations,
			oldAllocations,
		)

		return nil, err
	}

	return gameCell, nil
}

func cellAllocations(gameCell *Cell) []allocation.Allocation {
	allocations := make([]allocation.Allocation, 0, 1+len(gameCell.AdditionalAllocations))

	allocations = append(allocations, gameCell.Allocation)
	allocations = append(allocations, gameCell.AdditionalAllocations...)

	return allocations
}
