package cell

import "hivepanel-worker/internal/allocation"

func (m *Manager) ReconfigureAllocations(allocations []allocation.Allocation) ([]allocation.Allocation, error) {
	return m.allocManager.Reconfigure(allocations)
}

func (m *Manager) AllocationConfiguration() []allocation.Allocation {
	return m.allocManager.Configuration()
}
