package cell

func (m *Manager) ReconfigureAllocations(ips []string, portStart int, portEnd int) ([]string, error) {
	return m.allocManager.Reconfigure(
		ips,
		portStart,
		portEnd,
	)
}

func (m *Manager) AllocationConfiguration() ([]string, int, int) {
	return m.allocManager.Configuration()
}
