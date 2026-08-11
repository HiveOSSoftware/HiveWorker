package cell

import (
	"errors"
	"fmt"
	"os"

	"hivepanel-worker/internal/install"
)

func (m *Manager) Install(id string) error {
	m.mutex.Lock()

	gameCell, exists := m.cells[id]
	if !exists {
		m.mutex.Unlock()
		return errors.New("cell not found")
	}

	if m.runtime.IsRunning(id) {
		m.mutex.Unlock()
		return errors.New("cell must be stopped before installing")
	}

	selectedComb, err := m.resolveCellComb(gameCell)
	if err != nil {
		m.mutex.Unlock()
		return err
	}

	m.broadcast(gameCell, "Install started.")

	dir := gameCell.Dir
	steps := selectedComb.Install
	variables := gameCell.Variables

	m.mutex.Unlock()

	err = install.Run(
		dir,
		variables,
		steps,
		func(line string) {
			m.mutex.Lock()

			if gameCell, exists := m.cells[id]; exists {
				m.broadcast(gameCell, line)
			}

			m.mutex.Unlock()
		},
	)

	m.mutex.Lock()
	defer m.mutex.Unlock()

	if gameCell, exists := m.cells[id]; exists {
		if err != nil {
			m.broadcast(
				gameCell,
				"Install failed: "+err.Error(),
			)
		} else {
			m.broadcast(
				gameCell,
				"Install completed.",
			)
		}
	}

	return err
}

func (m *Manager) Reinstall(id string) error {
	m.mutex.Lock()

	gameCell, exists := m.cells[id]
	if !exists {
		m.mutex.Unlock()
		return errors.New("cell not found")
	}

	if m.runtime.IsRunning(id) {
		m.mutex.Unlock()
		return errors.New("cell must be stopped before reinstalling")
	}

	dir := gameCell.Dir

	m.broadcast(
		gameCell,
		"Preparing cell for reinstall.",
	)

	m.mutex.Unlock()

	if dir == "" {
		return errors.New("cell directory is empty")
	}

	info, err := os.Stat(dir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf(
			"stat cell directory: %w",
			err,
		)
	}

	if err == nil && !info.IsDir() {
		return errors.New("cell path is not a directory")
	}

	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf(
			"remove existing cell files: %w",
			err,
		)
	}

	if err := os.MkdirAll(
		dir,
		0750,
	); err != nil {
		return fmt.Errorf(
			"recreate cell directory: %w",
			err,
		)
	}

	m.mutex.Lock()

	if gameCell, exists := m.cells[id]; exists {
		m.broadcast(
			gameCell,
			"Cell is ready for reinstall.",
		)
	}

	m.mutex.Unlock()

	return nil
}
