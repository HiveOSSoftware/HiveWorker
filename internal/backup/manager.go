package backup

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var (
	ErrInvalidBackupID   = errors.New("invalid backup id")
	ErrBackupNotFound    = errors.New("backup not found")
	ErrInvalidBackupPath = errors.New("invalid backup path")
)

const backupExtension = ".tar.gz"

type Backup struct {
	ID                string `json:"backup_id"`
	Name              string `json:"name"`
	ArchiveName       string `json:"archive_name"`
	Path              string `json:"path"`
	Size              int64  `json:"size"`
	Checksum          string `json:"checksum"`
	ChecksumAlgorithm string `json:"checksum_algorithm"`
	CreatedAt         string `json:"created_at"`
	CompletedAt       string `json:"completed_at"`
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

// Create creates a tar.gz archive using Laravel's backup UUID.
//
// The resulting archive is:
//
//	<backupsDir>/<cellID>/<backupID>.tar.gz
func (m *Manager) Create(
	cellID string,
	backupID string,
	displayName string,
	sourceDir string,
	ignoredFiles []string,
) (*Backup, error) {
	if err := validateIdentifier(cellID); err != nil {
		return nil, fmt.Errorf("invalid cell id: %w", err)
	}

	if err := validateIdentifier(backupID); err != nil {
		return nil, ErrInvalidBackupID
	}

	sourceInfo, err := os.Stat(sourceDir)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect backup source: %w", err)
	}

	if !sourceInfo.IsDir() {
		return nil, errors.New("backup source is not a directory")
	}

	cellBackupDir, err := m.cellBackupDirectory(cellID)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(cellBackupDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create backup directory: %w", err)
	}

	archiveName := backupID + backupExtension
	targetPath := filepath.Join(cellBackupDir, archiveName)

	if _, err := os.Stat(targetPath); err == nil {
		return nil, fmt.Errorf("backup archive already exists: %s", archiveName)
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	if err := tarGzipFolder(
		sourceDir,
		targetPath,
		normaliseIgnoredFiles(ignoredFiles),
	); err != nil {
		_ = os.Remove(targetPath)
		return nil, fmt.Errorf("failed to create backup archive: %w", err)
	}

	info, err := os.Stat(targetPath)
	if err != nil {
		_ = os.Remove(targetPath)
		return nil, fmt.Errorf("failed to inspect created backup: %w", err)
	}

	checksum, err := calculateSHA256(targetPath)
	if err != nil {
		_ = os.Remove(targetPath)
		return nil, fmt.Errorf("failed to calculate backup checksum: %w", err)
	}

	completedAt := info.ModTime().UTC().Format(time.RFC3339)

	return &Backup{
		ID:                backupID,
		Name:              displayName,
		ArchiveName:       archiveName,
		Path:              filepath.ToSlash(filepath.Join(cellID, archiveName)),
		Size:              info.Size(),
		Checksum:          checksum,
		ChecksumAlgorithm: "sha256",
		CreatedAt:         completedAt,
		CompletedAt:       completedAt,
	}, nil
}

func (m *Manager) List(cellID string) ([]Backup, error) {
	cellBackupDir, err := m.cellBackupDirectory(cellID)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(cellBackupDir)
	if os.IsNotExist(err) {
		return []Backup{}, nil
	}
	if err != nil {
		return nil, err
	}

	backups := make([]Backup, 0, len(entries))

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), backupExtension) {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		backupID := strings.TrimSuffix(entry.Name(), backupExtension)
		timestamp := info.ModTime().UTC().Format(time.RFC3339)

		backups = append(backups, Backup{
			ID:          backupID,
			Name:        backupID,
			ArchiveName: entry.Name(),
			Path:        filepath.ToSlash(filepath.Join(cellID, entry.Name())),
			Size:        info.Size(),
			CreatedAt:   timestamp,
			CompletedAt: timestamp,
		})
	}

	sort.Slice(backups, func(i int, j int) bool {
		return backups[i].CreatedAt > backups[j].CreatedAt
	})

	return backups, nil
}

func (m *Manager) Delete(cellID string, backupID string) error {
	path, err := m.SafeBackupPath(cellID, backupID)
	if err != nil {
		return err
	}

	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return ErrBackupNotFound
		}
		return err
	}

	return nil
}

func (m *Manager) Restore(
	cellID string,
	backupID string,
	targetDir string,
) error {
	backupPath, err := m.SafeBackupPath(cellID, backupID)
	if err != nil {
		return err
	}

	if err := os.RemoveAll(targetDir); err != nil {
		return fmt.Errorf("failed to clear restore directory: %w", err)
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create restore directory: %w", err)
	}

	if err := extractTarGzip(backupPath, targetDir); err != nil {
		return fmt.Errorf("failed to restore backup: %w", err)
	}

	return nil
}

func (m *Manager) DownloadPath(
	cellID string,
	backupID string,
) (string, error) {
	return m.SafeBackupPath(cellID, backupID)
}

func (m *Manager) SafeBackupPath(
	cellID string,
	backupID string,
) (string, error) {
	if err := validateIdentifier(cellID); err != nil {
		return "", fmt.Errorf("invalid cell id: %w", err)
	}

	if err := validateIdentifier(backupID); err != nil {
		return "", ErrInvalidBackupID
	}

	baseDir, err := m.cellBackupDirectory(cellID)
	if err != nil {
		return "", err
	}

	fullPath := filepath.Join(baseDir, backupID+backupExtension)

	if err := ensurePathWithinBase(baseDir, fullPath); err != nil {
		return "", ErrInvalidBackupPath
	}

	info, err := os.Stat(fullPath)
	if os.IsNotExist(err) {
		return "", ErrBackupNotFound
	}
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", ErrBackupNotFound
	}

	return fullPath, nil
}

func (m *Manager) ListFiles(
	cellID string,
	backupID string,
) ([]BackupFileEntry, error) {
	backupPath, err := m.SafeBackupPath(cellID, backupID)
	if err != nil {
		return nil, err
	}

	file, gzipReader, tarReader, err := openTarGzip(backupPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	defer gzipReader.Close()

	files := make([]BackupFileEntry, 0)

	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}

		cleanPath, err := cleanArchivePath(header.Name)
		if err != nil {
			continue
		}

		isDir := header.Typeflag == tar.TypeDir

		files = append(files, BackupFileEntry{
			Name:  filepath.Base(filepath.FromSlash(cleanPath)),
			Path:  cleanPath,
			IsDir: isDir,
			Size:  header.Size,
		})
	}

	return files, nil
}

func (m *Manager) ReadFile(
	cellID string,
	backupID string,
	requestedPath string,
) (string, error) {
	backupPath, err := m.SafeBackupPath(cellID, backupID)
	if err != nil {
		return "", err
	}

	requestedPath, err = cleanRequestedArchivePath(requestedPath)
	if err != nil {
		return "", err
	}

	file, gzipReader, tarReader, err := openTarGzip(backupPath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	defer gzipReader.Close()

	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}

		archivePath, err := cleanArchivePath(header.Name)
		if err != nil || archivePath != requestedPath {
			continue
		}

		if header.Typeflag == tar.TypeDir {
			return "", errors.New("cannot read a folder")
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return "", errors.New("cannot read this archive entry type")
		}

		data, err := io.ReadAll(tarReader)
		if err != nil {
			return "", err
		}

		return string(data), nil
	}

	return "", errors.New("file not found in backup")
}

func (m *Manager) ExtractPath(
	cellID string,
	backupID string,
	requestedPath string,
	targetDir string,
) error {
	backupPath, err := m.SafeBackupPath(cellID, backupID)
	if err != nil {
		return err
	}

	requestedPath, err = cleanRequestedArchivePath(requestedPath)
	if err != nil {
		return err
	}

	targetAbs, err := filepath.Abs(targetDir)
	if err != nil {
		return err
	}

	file, gzipReader, tarReader, err := openTarGzip(backupPath)
	if err != nil {
		return err
	}
	defer file.Close()
	defer gzipReader.Close()

	found := false

	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}

		archivePath, err := cleanArchivePath(header.Name)
		if err != nil {
			return err
		}

		if archivePath != requestedPath &&
			!strings.HasPrefix(archivePath, requestedPath+"/") {
			continue
		}

		found = true

		targetPath := filepath.Join(
			targetAbs,
			filepath.FromSlash(archivePath),
		)

		if err := ensurePathWithinBase(targetAbs, targetPath); err != nil {
			return errors.New("invalid file path in backup")
		}

		if err := extractTarEntry(tarReader, header, targetPath); err != nil {
			return err
		}
	}

	if !found {
		return errors.New("path not found in backup")
	}

	return nil
}

func (m *Manager) cellBackupDirectory(
	cellID string,
) (string, error) {
	if err := validateIdentifier(cellID); err != nil {
		return "", err
	}

	return filepath.Join(m.backupsDir, cellID), nil
}

func tarGzipFolder(
	sourceDir string,
	targetPath string,
	ignoredFiles []string,
) (returnErr error) {
	out, err := os.OpenFile(
		targetPath,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		0644,
	)
	if err != nil {
		return err
	}

	defer func() {
		if err := out.Close(); returnErr == nil && err != nil {
			returnErr = err
		}
	}()

	gzipWriter := gzip.NewWriter(out)

	defer func() {
		if err := gzipWriter.Close(); returnErr == nil && err != nil {
			returnErr = err
		}
	}()

	tarWriter := tar.NewWriter(gzipWriter)

	defer func() {
		if err := tarWriter.Close(); returnErr == nil && err != nil {
			returnErr = err
		}
	}()

	sourceDirAbs, err := filepath.Abs(sourceDir)
	if err != nil {
		return err
	}

	return filepath.Walk(
		sourceDirAbs,
		func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}

			if path == sourceDirAbs {
				return nil
			}

			relativePath, err := filepath.Rel(sourceDirAbs, path)
			if err != nil {
				return err
			}

			relativePath = filepath.ToSlash(filepath.Clean(relativePath))

			if isIgnoredPath(relativePath, ignoredFiles) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			// Backups intentionally skip symbolic links. This avoids restoring
			// links that could point outside the cell's filesystem.
			if info.Mode()&os.ModeSymlink != 0 {
				return nil
			}

			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}

			header.Name = relativePath

			if info.IsDir() && !strings.HasSuffix(header.Name, "/") {
				header.Name += "/"
			}

			if err := tarWriter.WriteHeader(header); err != nil {
				return err
			}

			if !info.Mode().IsRegular() {
				return nil
			}

			file, err := os.Open(path)
			if err != nil {
				return err
			}

			_, copyErr := io.Copy(tarWriter, file)
			closeErr := file.Close()

			if copyErr != nil {
				return copyErr
			}

			return closeErr
		},
	)
}

func extractTarGzip(
	archivePath string,
	targetDir string,
) error {
	targetAbs, err := filepath.Abs(targetDir)
	if err != nil {
		return err
	}

	file, gzipReader, tarReader, err := openTarGzip(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	defer gzipReader.Close()

	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}

		archiveEntryPath, err := cleanArchivePath(header.Name)
		if err != nil {
			return err
		}

		targetPath := filepath.Join(
			targetAbs,
			filepath.FromSlash(archiveEntryPath),
		)

		if err := ensurePathWithinBase(targetAbs, targetPath); err != nil {
			return errors.New("invalid file path in backup")
		}

		if err := extractTarEntry(tarReader, header, targetPath); err != nil {
			return err
		}
	}

	return nil
}

func openTarGzip(
	archivePath string,
) (*os.File, *gzip.Reader, *tar.Reader, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return nil, nil, nil, err
	}

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		file.Close()
		return nil, nil, nil, err
	}

	return file, gzipReader, tar.NewReader(gzipReader), nil
}

func extractTarEntry(
	reader *tar.Reader,
	header *tar.Header,
	targetPath string,
) error {
	switch header.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(
			targetPath,
			safeDirectoryMode(os.FileMode(header.Mode)),
		)

	case tar.TypeReg, tar.TypeRegA:
		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return err
		}

		file, err := os.OpenFile(
			targetPath,
			os.O_WRONLY|os.O_CREATE|os.O_TRUNC,
			safeFileMode(os.FileMode(header.Mode)),
		)
		if err != nil {
			return err
		}

		_, copyErr := io.Copy(file, reader)
		closeErr := file.Close()

		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}

		modTime := header.ModTime
		if !modTime.IsZero() {
			_ = os.Chtimes(targetPath, modTime, modTime)
		}

		return nil

	case tar.TypeSymlink, tar.TypeLink:
		return errors.New("backup contains a symbolic or hard link")

	default:
		// Ignore device files, sockets, FIFOs, and other unsupported types.
		return nil
	}
}

func calculateSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()

	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func validateIdentifier(value string) error {
	value = strings.TrimSpace(value)

	if value == "" {
		return errors.New("identifier is required")
	}
	if len(value) > 128 {
		return errors.New("identifier is too long")
	}
	if value == "." || value == ".." {
		return errors.New("invalid identifier")
	}

	for _, character := range value {
		valid := character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '-' ||
			character == '_'

		if !valid {
			return errors.New("identifier contains invalid characters")
		}
	}

	return nil
}

func normaliseIgnoredFiles(ignoredFiles []string) []string {
	seen := make(map[string]struct{})
	normalised := make([]string, 0, len(ignoredFiles))

	for _, ignoredPath := range ignoredFiles {
		ignoredPath = strings.TrimSpace(
			strings.ReplaceAll(ignoredPath, "\\", "/"),
		)

		ignoredPath = strings.Trim(
			filepath.ToSlash(filepath.Clean(ignoredPath)),
			"/",
		)

		if ignoredPath == "" ||
			ignoredPath == "." ||
			ignoredPath == ".." ||
			strings.HasPrefix(ignoredPath, "../") {
			continue
		}

		if _, exists := seen[ignoredPath]; exists {
			continue
		}

		seen[ignoredPath] = struct{}{}
		normalised = append(normalised, ignoredPath)
	}

	sort.Strings(normalised)

	return normalised
}

func isIgnoredPath(path string, ignoredFiles []string) bool {
	path = strings.Trim(
		filepath.ToSlash(filepath.Clean(path)),
		"/",
	)

	for _, ignoredPath := range ignoredFiles {
		if path == ignoredPath ||
			strings.HasPrefix(path, ignoredPath+"/") {
			return true
		}
	}

	return false
}

func cleanArchivePath(path string) (string, error) {
	path = strings.ReplaceAll(path, "\\", "/")
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, "/")
	path = filepath.ToSlash(filepath.Clean(path))

	if path == "." ||
		path == "" ||
		path == ".." ||
		strings.HasPrefix(path, "../") ||
		strings.Contains(path, "\x00") {
		return "", errors.New("invalid file path in backup")
	}

	return path, nil
}

func cleanRequestedArchivePath(path string) (string, error) {
	path = strings.TrimSpace(path)

	if path == "" {
		return "", errors.New("backup path is required")
	}

	return cleanArchivePath(path)
}

func ensurePathWithinBase(basePath string, targetPath string) error {
	absBase, err := filepath.Abs(basePath)
	if err != nil {
		return err
	}

	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return err
	}

	relative, err := filepath.Rel(absBase, absTarget)
	if err != nil {
		return err
	}

	if relative == ".." ||
		strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return ErrInvalidBackupPath
	}

	return nil
}

func safeFileMode(mode os.FileMode) os.FileMode {
	mode &= os.ModePerm

	if mode == 0 {
		return 0644
	}

	return mode
}

func safeDirectoryMode(mode os.FileMode) os.FileMode {
	mode &= os.ModePerm

	if mode == 0 {
		return 0755
	}

	return mode
}
