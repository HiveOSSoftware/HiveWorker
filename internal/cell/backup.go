package cell

import "errors"

func (m *Manager) CreateBackup(id string) (any, error) {
	m.mutex.Lock()

	cell, exists := m.cells[id]
	if !exists {
		m.mutex.Unlock()
		return nil, errors.New("cell not found")
	}

	dir := cell.Dir
	m.mutex.Unlock()

	return m.backupManager.Create(id, dir)
}

func (m *Manager) ListBackups(id string) (any, error) {
	m.mutex.Lock()

	_, exists := m.cells[id]
	if !exists {
		m.mutex.Unlock()
		return nil, errors.New("cell not found")
	}

	m.mutex.Unlock()

	return m.backupManager.List(id)
}

func (m *Manager) DeleteBackup(id string, name string) error {
	m.mutex.Lock()

	_, exists := m.cells[id]
	if !exists {
		m.mutex.Unlock()
		return errors.New("cell not found")
	}

	m.mutex.Unlock()

	return m.backupManager.Delete(id, name)
}

func (m *Manager) BackupDownloadPath(id string, name string) (string, error) {
	m.mutex.Lock()

	_, exists := m.cells[id]
	if !exists {
		m.mutex.Unlock()
		return "", errors.New("cell not found")
	}

	m.mutex.Unlock()

	return m.backupManager.DownloadPath(id, name)
}

func (m *Manager) RestoreBackup(id string, name string) error {
	m.mutex.Lock()

	cell, exists := m.cells[id]
	if !exists {
		m.mutex.Unlock()
		return errors.New("cell not found")
	}

	if m.runtime.IsRunning(id) {
		m.mutex.Unlock()
		return errors.New("cell must be stopped before restoring backup")
	}

	dir := cell.Dir
	m.mutex.Unlock()

	return m.backupManager.Restore(id, name, dir)
}

func (m *Manager) ListBackupFiles(id string, name string) (any, error) {
	m.mutex.Lock()

	_, exists := m.cells[id]
	if !exists {
		m.mutex.Unlock()
		return nil, errors.New("cell not found")
	}

	m.mutex.Unlock()

	return m.backupManager.ListFiles(id, name)
}

func (m *Manager) ReadBackupFile(id string, name string, path string) (string, error) {
	m.mutex.Lock()

	_, exists := m.cells[id]
	if !exists {
		m.mutex.Unlock()
		return "", errors.New("cell not found")
	}

	m.mutex.Unlock()

	return m.backupManager.ReadFile(id, name, path)
}

func (m *Manager) ExtractBackupPath(id string, name string, path string) error {
	m.mutex.Lock()

	cell, exists := m.cells[id]
	if !exists {
		m.mutex.Unlock()
		return errors.New("cell not found")
	}

	if m.runtime.IsRunning(id) {
		m.mutex.Unlock()
		return errors.New("cell must be stopped before extracting from backup")
	}

	dir := cell.Dir
	m.mutex.Unlock()

	return m.backupManager.ExtractPath(id, name, path, dir)
}
