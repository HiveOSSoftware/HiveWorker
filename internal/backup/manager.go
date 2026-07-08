package backup

import (
	"archive/zip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Backup struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	CreatedAt string `json:"created_at"`
}

type BackupFileEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

type Manager struct {
	backupsDir string
}

func NewManager(backupsDir string) *Manager {
	return &Manager{
		backupsDir: backupsDir,
	}
}

func (m *Manager) Create(cellID string, sourceDir string) (*Backup, error) {
	cellBackupDir := filepath.Join(m.backupsDir, cellID)

	if err := os.MkdirAll(cellBackupDir, 0755); err != nil {
		return nil, err
	}

	name := time.Now().UTC().Format("20060102-150405") + ".zip"
	targetPath := filepath.Join(cellBackupDir, name)

	if err := zipFolder(sourceDir, targetPath); err != nil {
		return nil, err
	}

	info, err := os.Stat(targetPath)
	if err != nil {
		return nil, err
	}

	return &Backup{
		Name:      name,
		Path:      filepath.ToSlash(filepath.Join(cellID, name)),
		Size:      info.Size(),
		CreatedAt: info.ModTime().UTC().Format(time.RFC3339),
	}, nil
}

func (m *Manager) List(cellID string) ([]Backup, error) {
	cellBackupDir := filepath.Join(m.backupsDir, cellID)

	entries, err := os.ReadDir(cellBackupDir)
	if os.IsNotExist(err) {
		return []Backup{}, nil
	}

	if err != nil {
		return nil, err
	}

	backups := []Backup{}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".zip" {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		backups = append(backups, Backup{
			Name:      entry.Name(),
			Path:      filepath.ToSlash(filepath.Join(cellID, entry.Name())),
			Size:      info.Size(),
			CreatedAt: info.ModTime().UTC().Format(time.RFC3339),
		})
	}

	return backups, nil
}

func (m *Manager) Delete(cellID string, name string) error {
	path, err := m.SafeBackupPath(cellID, name)
	if err != nil {
		return err
	}

	return os.Remove(path)
}

func (m *Manager) Restore(cellID string, name string, targetDir string) error {
	backupPath, err := m.SafeBackupPath(cellID, name)
	if err != nil {
		return err
	}

	if err := os.RemoveAll(targetDir); err != nil {
		return err
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return err
	}

	return unzipToFolder(backupPath, targetDir)
}

func (m *Manager) DownloadPath(cellID string, name string) (string, error) {
	return m.SafeBackupPath(cellID, name)
}

func (m *Manager) SafeBackupPath(cellID string, name string) (string, error) {
	if filepath.Ext(name) != ".zip" {
		return "", errors.New("invalid backup name")
	}

	baseDir := filepath.Join(m.backupsDir, cellID)
	fullPath := filepath.Join(baseDir, name)

	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}

	absFull, err := filepath.Abs(fullPath)
	if err != nil {
		return "", err
	}

	if !strings.HasPrefix(absFull, absBase) {
		return "", errors.New("invalid backup path")
	}

	if _, err := os.Stat(absFull); err != nil {
		return "", err
	}

	return absFull, nil
}

func zipFolder(sourceDir string, targetPath string) error {
	out, err := os.Create(targetPath)
	if err != nil {
		return err
	}
	defer out.Close()

	writer := zip.NewWriter(out)
	defer writer.Close()

	sourceDirAbs, err := filepath.Abs(sourceDir)
	if err != nil {
		return err
	}

	return filepath.Walk(sourceDirAbs, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if info.IsDir() {
			return nil
		}

		relativePath, err := filepath.Rel(sourceDirAbs, path)
		if err != nil {
			return err
		}

		relativePath = filepath.ToSlash(relativePath)

		if strings.HasPrefix(relativePath, "../") {
			return nil
		}

		zipFile, err := writer.Create(relativePath)
		if err != nil {
			return err
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(zipFile, file)
		return err
	})
}

func unzipToFolder(zipPath string, targetDir string) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	targetAbs, err := filepath.Abs(targetDir)
	if err != nil {
		return err
	}

	for _, file := range reader.File {
		targetPath := filepath.Join(targetAbs, file.Name)

		targetPathAbs, err := filepath.Abs(targetPath)
		if err != nil {
			return err
		}

		if !strings.HasPrefix(targetPathAbs, targetAbs) {
			return errors.New("invalid file path in backup")
		}

		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPathAbs, file.Mode()); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPathAbs), 0755); err != nil {
			return err
		}

		src, err := file.Open()
		if err != nil {
			return err
		}

		dst, err := os.OpenFile(targetPathAbs, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
		if err != nil {
			src.Close()
			return err
		}

		_, err = io.Copy(dst, src)

		src.Close()
		dst.Close()

		if err != nil {
			return err
		}
	}

	return nil
}

func (m *Manager) ListFiles(cellID string, name string) ([]BackupFileEntry, error) {
	backupPath, err := m.SafeBackupPath(cellID, name)
	if err != nil {
		return nil, err
	}

	reader, err := zip.OpenReader(backupPath)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	files := []BackupFileEntry{}

	for _, file := range reader.File {
		files = append(files, BackupFileEntry{
			Name:  filepath.Base(file.Name),
			Path:  filepath.ToSlash(file.Name),
			IsDir: file.FileInfo().IsDir(),
			Size:  int64(file.UncompressedSize64),
		})
	}

	return files, nil
}

func (m *Manager) ReadFile(cellID string, name string, requestedPath string) (string, error) {
	backupPath, err := m.SafeBackupPath(cellID, name)
	if err != nil {
		return "", err
	}

	reader, err := zip.OpenReader(backupPath)
	if err != nil {
		return "", err
	}
	defer reader.Close()

	cleanPath := filepath.ToSlash(filepath.Clean(requestedPath))

	for _, file := range reader.File {
		if filepath.ToSlash(file.Name) != cleanPath {
			continue
		}

		if file.FileInfo().IsDir() {
			return "", errors.New("cannot read a folder")
		}

		src, err := file.Open()
		if err != nil {
			return "", err
		}
		defer src.Close()

		data, err := io.ReadAll(src)
		if err != nil {
			return "", err
		}

		return string(data), nil
	}

	return "", errors.New("file not found in backup")
}

func (m *Manager) ExtractPath(cellID string, name string, requestedPath string, targetDir string) error {
	backupPath, err := m.SafeBackupPath(cellID, name)
	if err != nil {
		return err
	}

	reader, err := zip.OpenReader(backupPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	cleanPath := filepath.ToSlash(filepath.Clean(requestedPath))
	targetAbs, err := filepath.Abs(targetDir)
	if err != nil {
		return err
	}

	found := false

	for _, file := range reader.File {
		zipPath := filepath.ToSlash(file.Name)

		if zipPath != cleanPath && !strings.HasPrefix(zipPath, cleanPath+"/") {
			continue
		}

		found = true

		targetPath := filepath.Join(targetAbs, zipPath)
		targetPathAbs, err := filepath.Abs(targetPath)
		if err != nil {
			return err
		}

		if !strings.HasPrefix(targetPathAbs, targetAbs) {
			return errors.New("invalid file path in backup")
		}

		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPathAbs, file.Mode()); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPathAbs), 0755); err != nil {
			return err
		}

		src, err := file.Open()
		if err != nil {
			return err
		}

		dst, err := os.OpenFile(targetPathAbs, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
		if err != nil {
			src.Close()
			return err
		}

		_, err = io.Copy(dst, src)

		src.Close()
		dst.Close()

		if err != nil {
			return err
		}
	}

	if !found {
		return errors.New("path not found in backup")
	}

	return nil
}
