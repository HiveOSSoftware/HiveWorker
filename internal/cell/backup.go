package cell

import "errors"

func (m *Manager) CreateBackup(
	id string,
	backupID string,
	name string,
	ignoredFiles []string,
) (any, error) {
	cell, exists := m.getCellForBackup(id)
	if !exists {
		return nil, errors.New("cell not found")
	}

	return m.backupManager.Create(
		id,
		backupID,
		name,
		cell.Dir,
		ignoredFiles,
	)
}

func (m *Manager) ListBackups(id string) (any, error) {
	if _, exists := m.getCellForBackup(id); !exists {
		return nil, errors.New("cell not found")
	}

	return m.backupManager.List(id)
}

func (m *Manager) DeleteBackup(
	id string,
	backupID string,
) error {
	if _, exists := m.getCellForBackup(id); !exists {
		return errors.New("cell not found")
	}

	return m.backupManager.Delete(id, backupID)
}

func (m *Manager) BackupDownloadPath(
	id string,
	backupID string,
) (string, error) {
	if _, exists := m.getCellForBackup(id); !exists {
		return "", errors.New("cell not found")
	}

	return m.backupManager.DownloadPath(id, backupID)
}

func (m *Manager) RestoreBackup(
	id string,
	backupID string,
) error {
	cell, exists := m.getCellForBackup(id)
	if !exists {
		return errors.New("cell not found")
	}

	if m.runtime.IsRunning(id) {
		return errors.New(
			"cell must be stopped before restoring backup",
		)
	}

	return m.backupManager.Restore(
		id,
		backupID,
		cell.Dir,
	)
}

func (m *Manager) ListBackupFiles(
	id string,
	backupID string,
) (any, error) {
	if _, exists := m.getCellForBackup(id); !exists {
		return nil, errors.New("cell not found")
	}

	return m.backupManager.ListFiles(
		id,
		backupID,
	)
}

func (m *Manager) ReadBackupFile(
	id string,
	backupID string,
	path string,
) (string, error) {
	if _, exists := m.getCellForBackup(id); !exists {
		return "", errors.New("cell not found")
	}

	return m.backupManager.ReadFile(
		id,
		backupID,
		path,
	)
}

func (m *Manager) ExtractBackupPath(
	id string,
	backupID string,
	path string,
) error {
	cell, exists := m.getCellForBackup(id)
	if !exists {
		return errors.New("cell not found")
	}

	if m.runtime.IsRunning(id) {
		return errors.New(
			"cell must be stopped before extracting from backup",
		)
	}

	return m.backupManager.ExtractPath(
		id,
		backupID,
		path,
		cell.Dir,
	)
}

func (m *Manager) getCellForBackup(
	id string,
) (*Cell, bool) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	cell, exists := m.cells[id]

	return cell, exists
}
