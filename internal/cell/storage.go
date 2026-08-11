package cell

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"hivepanel-worker/internal/comb"
)

func (m *Manager) Create(request CreateCellRequest) (*Cell, error) {
	request.ID = strings.TrimSpace(request.ID)
	request.Name = strings.TrimSpace(request.Name)
	request.Comb = strings.TrimSpace(request.Comb)

	if request.Name == "" {
		return nil, errors.New("name is required")
	}

	if request.Comb == "" {
		return nil, errors.New("comb is required")
	}

	selectedComb, err := m.resolveComb(request)
	if err != nil {
		return nil, err
	}

	allocation := request.Allocation

	if allocation.IP == "" || allocation.Port <= 0 {
		return nil, errors.New("allocation is required")
	}

	variables := map[string]string{}

	for key, value := range selectedComb.Variables {
		variables[key] = value
	}

	for key, value := range request.Variables {
		variables[key] = value
	}

	id := uuid.NewString()

	if request.ID != "" {
		parsedID, err := uuid.Parse(request.ID)
		if err != nil {
			return nil, errors.New("invalid cell id")
		}

		id = parsedID.String()
	}

	m.mutex.RLock()
	_, exists := m.cells[id]
	m.mutex.RUnlock()

	if exists {
		return nil, errors.New("cell already exists")
	}

	dir := filepath.Join(m.instancesDir, id)

	if _, err := os.Stat(dir); err == nil {
		return nil, errors.New("cell directory already exists")
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	limits := request.Limits

	if limits.MemoryMB <= 0 {
		limits.MemoryMB = 1024
	}

	if limits.CPUPercent <= 0 {
		limits.CPUPercent = 100
	}

	gameCell := &Cell{
		ID:         id,
		Name:       request.Name,
		Comb:       selectedComb.ID,
		CombData:   request.CombData,
		Variables:  variables,
		Dir:        dir,
		Allocation: allocation,
		Limits:     limits,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
		Status:     "offline",

		console:     []string{},
		subscribers: map[chan string]bool{},
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		m.allocManager.Release(allocation)
		return nil, err
	}

	if err := m.save(gameCell); err != nil {
		_ = os.RemoveAll(dir)
		m.allocManager.Release(allocation)
		return nil, err
	}

	m.mutex.Lock()
	m.cells[id] = gameCell
	m.mutex.Unlock()

	return gameCell, nil
}

func (m *Manager) Delete(id string) error {
	m.mutex.Lock()

	cell, exists := m.cells[id]
	if !exists {
		m.mutex.Unlock()
		return errors.New("cell not found")
	}

	if m.runtime.IsRunning(id) {
		m.mutex.Unlock()
		return errors.New("cell must be stopped first")
	}

	m.allocManager.Release(cell.Allocation)

	delete(m.cells, id)
	m.mutex.Unlock()

	_ = os.Remove(m.cellConfigPath(id))

	return os.RemoveAll(cell.Dir)
}

func (m *Manager) save(cell *Cell) error {
	if err := os.MkdirAll(m.cellDataDir(), 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cell, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(m.cellConfigPath(cell.ID), data, 0644)
}

func (m *Manager) cellDataDir() string {
	return filepath.Join(m.dataDir, "cells")
}

func (m *Manager) cellConfigPath(id string) string {
	return filepath.Join(m.cellDataDir(), id+".json")
}

func (m *Manager) resolveComb(request CreateCellRequest) (*comb.Comb, error) {
	if request.CombData != nil {
		return comb.FromMap(request.CombData)
	}

	return m.combManager.Require(request.Comb)
}

func (m *Manager) CellDir(id string) (string, error) {
	id = strings.TrimSpace(id)

	if id == "" {
		return "", errors.New("cell id is required")
	}

	m.mutex.RLock()
	defer m.mutex.RUnlock()

	gameCell, exists := m.cells[id]
	if !exists {
		return "", errors.New("cell not found")
	}

	if strings.TrimSpace(gameCell.Dir) == "" {
		return "", errors.New("cell directory is unavailable")
	}

	return gameCell.Dir, nil
}

func folderSize(path string) (int64, error) {
	var size int64

	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if !info.IsDir() {
			size += info.Size()
		}

		return nil
	})

	return size, err
}
