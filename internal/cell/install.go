package cell

import (
	"errors"
	"hivepanel-worker/internal/install"
)

func (m *Manager) Install(id string) error {
	m.mutex.Lock()

	cell, exists := m.cells[id]
	if !exists {
		m.mutex.Unlock()
		return errors.New("cell not found")
	}

	if m.runtime.IsRunning(id) {
		m.mutex.Unlock()
		return errors.New("cell must be stopped before installing")
	}

	selectedComb, err := m.resolveCellComb(cell)
	if err != nil {
		m.mutex.Unlock()
		return err
	}
	m.broadcast(cell, "Install started.")

	dir := cell.Dir
	steps := selectedComb.Install

	m.mutex.Unlock()

	err = install.Run(dir, cell.Variables, steps, func(line string) {
		m.mutex.Lock()
		if cell, exists := m.cells[id]; exists {
			m.broadcast(cell, line)
		}
		m.mutex.Unlock()
	})

	m.mutex.Lock()
	defer m.mutex.Unlock()

	if cell, exists := m.cells[id]; exists {
		if err != nil {
			m.broadcast(cell, "Install failed: "+err.Error())
		} else {
			m.broadcast(cell, "Install completed.")
		}
	}

	return err
}
