package backup

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultDirectoryMode = 0o750
	defaultFileMode      = 0o640
)

// The worker creates new backups using this extension.
//
// Additional readable extensions can be added to
// supportedBackupExtensions without changing the mount logic.
const primaryBackupExtension = ".tgz"

var supportedBackupExtensions = []string{
	primaryBackupExtension,
	".tar.gz",
}

var (
	ErrInvalidMountID     = errors.New("invalid backup mount id")
	ErrMountAlreadyExists = errors.New("backup mount already exists")
	ErrUnsafeArchivePath  = errors.New("unsafe path found in backup archive")
	ErrUnsupportedEntry   = errors.New("unsupported entry found in backup archive")
)

type MountService struct {
	backupsRoot string
	mountsRoot  string
}

type MountResult struct {
	Path          string `json:"path"`
	ExtractedSize int64  `json:"extracted_size"`
}

func NewMountService(
	backupsRoot string,
	mountsRoot string,
) (*MountService, error) {
	if strings.TrimSpace(backupsRoot) == "" {
		return nil, errors.New("backups root cannot be empty")
	}

	if strings.TrimSpace(mountsRoot) == "" {
		return nil, errors.New("backup mounts root cannot be empty")
	}

	absoluteBackupsRoot, err := filepath.Abs(backupsRoot)
	if err != nil {
		return nil, fmt.Errorf(
			"resolve backups root: %w",
			err,
		)
	}

	absoluteMountsRoot, err := filepath.Abs(mountsRoot)
	if err != nil {
		return nil, fmt.Errorf(
			"resolve backup mounts root: %w",
			err,
		)
	}

	if err := os.MkdirAll(
		absoluteBackupsRoot,
		defaultDirectoryMode,
	); err != nil {
		return nil, fmt.Errorf(
			"create backups root: %w",
			err,
		)
	}

	if err := os.MkdirAll(
		absoluteMountsRoot,
		defaultDirectoryMode,
	); err != nil {
		return nil, fmt.Errorf(
			"create backup mounts root: %w",
			err,
		)
	}

	return &MountService{
		backupsRoot: absoluteBackupsRoot,
		mountsRoot:  absoluteMountsRoot,
	}, nil
}

func (service *MountService) Mount(
	cellID string,
	backupID string,
	mountID string,
) (*MountResult, error) {
	if !isSafeIdentifier(cellID) {
		return nil, fmt.Errorf(
			"cell id: %w",
			ErrInvalidMountID,
		)
	}

	if !isSafeIdentifier(backupID) {
		return nil, ErrInvalidBackupID
	}

	if !isSafeIdentifier(mountID) {
		return nil, ErrInvalidMountID
	}

	archivePath, err := service.resolveBackupArchive(
		cellID,
		backupID,
	)
	if err != nil {
		return nil, err
	}

	mountPath, err := service.resolveMountPath(
		cellID,
		mountID,
	)
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(mountPath); err == nil {
		return nil, ErrMountAlreadyExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf(
			"inspect mount directory: %w",
			err,
		)
	}

	if err := os.MkdirAll(
		filepath.Dir(mountPath),
		defaultDirectoryMode,
	); err != nil {
		return nil, fmt.Errorf(
			"create cell mount directory: %w",
			err,
		)
	}

	temporaryMountPath := mountPath + ".mounting"

	if err := os.RemoveAll(temporaryMountPath); err != nil {
		return nil, fmt.Errorf(
			"remove stale temporary mount directory: %w",
			err,
		)
	}

	if err := os.MkdirAll(
		temporaryMountPath,
		defaultDirectoryMode,
	); err != nil {
		return nil, fmt.Errorf(
			"create temporary mount directory: %w",
			err,
		)
	}

	extractedSize, err := extractTarGzipSecurely(
		archivePath,
		temporaryMountPath,
	)
	if err != nil {
		_ = os.RemoveAll(temporaryMountPath)

		return nil, fmt.Errorf(
			"extract backup archive: %w",
			err,
		)
	}

	if err := os.Rename(
		temporaryMountPath,
		mountPath,
	); err != nil {
		_ = os.RemoveAll(temporaryMountPath)

		return nil, fmt.Errorf(
			"finalise backup mount directory: %w",
			err,
		)
	}

	return &MountResult{
		Path:          mountPath,
		ExtractedSize: extractedSize,
	}, nil
}

func (service *MountService) Unmount(
	cellID string,
	mountID string,
) error {
	if !isSafeIdentifier(cellID) {
		return fmt.Errorf(
			"cell id: %w",
			ErrInvalidMountID,
		)
	}

	if !isSafeIdentifier(mountID) {
		return ErrInvalidMountID
	}

	mountPath, err := service.resolveMountPath(
		cellID,
		mountID,
	)
	if err != nil {
		return err
	}

	if err := os.RemoveAll(mountPath); err != nil {
		return fmt.Errorf(
			"remove backup mount: %w",
			err,
		)
	}

	temporaryMountPath := mountPath + ".mounting"

	if err := os.RemoveAll(temporaryMountPath); err != nil {
		return fmt.Errorf(
			"remove temporary backup mount: %w",
			err,
		)
	}

	return nil
}

func (service *MountService) MountPath(
	cellID string,
	mountID string,
) (string, error) {
	if !isSafeIdentifier(cellID) {
		return "", fmt.Errorf(
			"cell id: %w",
			ErrInvalidMountID,
		)
	}

	if !isSafeIdentifier(mountID) {
		return "", ErrInvalidMountID
	}

	mountPath, err := service.resolveMountPath(
		cellID,
		mountID,
	)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(mountPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", os.ErrNotExist
		}

		return "", fmt.Errorf(
			"inspect backup mount: %w",
			err,
		)
	}

	if !info.IsDir() {
		return "", errors.New(
			"backup mount path is not a directory",
		)
	}

	return mountPath, nil
}

func (service *MountService) resolveBackupArchive(
	cellID string,
	backupID string,
) (string, error) {
	if !isSafeIdentifier(cellID) {
		return "", ErrInvalidBackupID
	}

	if !isSafeIdentifier(backupID) {
		return "", ErrInvalidBackupID
	}

	cellBackupRoot := filepath.Join(
		service.backupsRoot,
		cellID,
	)

	for _, extension := range supportedBackupExtensions {
		archivePath := filepath.Join(
			cellBackupRoot,
			backupID+extension,
		)

		info, err := os.Stat(archivePath)
		if err == nil {
			if info.IsDir() {
				continue
			}

			return archivePath, nil
		}

		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf(
				"inspect backup archive: %w",
				err,
			)
		}
	}

	return "", ErrBackupNotFound
}

func (service *MountService) resolveMountPath(
	cellID string,
	mountID string,
) (string, error) {
	cellMountRoot := filepath.Join(
		service.mountsRoot,
		cellID,
	)

	mountPath := filepath.Join(
		cellMountRoot,
		mountID,
	)

	cleanRoot := filepath.Clean(cellMountRoot)
	cleanMountPath := filepath.Clean(mountPath)

	if cleanMountPath == cleanRoot {
		return "", ErrInvalidMountID
	}

	if !strings.HasPrefix(
		cleanMountPath,
		cleanRoot+string(os.PathSeparator),
	) {
		return "", ErrInvalidMountID
	}

	return cleanMountPath, nil
}

func extractTarGzipSecurely(
	archivePath string,
	destinationRoot string,
) (int64, error) {
	archiveFile, err := os.Open(archivePath)
	if err != nil {
		return 0, fmt.Errorf(
			"open archive: %w",
			err,
		)
	}
	defer archiveFile.Close()

	gzipReader, err := gzip.NewReader(archiveFile)
	if err != nil {
		return 0, fmt.Errorf(
			"open gzip stream: %w",
			err,
		)
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)

	cleanDestinationRoot, err := filepath.Abs(
		destinationRoot,
	)
	if err != nil {
		return 0, fmt.Errorf(
			"resolve destination directory: %w",
			err,
		)
	}

	var extractedSize int64

	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return extractedSize, fmt.Errorf(
				"read archive entry: %w",
				err,
			)
		}

		if header == nil {
			continue
		}

		entryPath, err := secureArchiveDestination(
			cleanDestinationRoot,
			header.Name,
		)
		if err != nil {
			return extractedSize, err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(
				entryPath,
				defaultDirectoryMode,
			); err != nil {
				return extractedSize, fmt.Errorf(
					"create archive directory %q: %w",
					header.Name,
					err,
				)
			}

		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 {
				return extractedSize, fmt.Errorf(
					"archive entry %q has a negative size",
					header.Name,
				)
			}

			if err := os.MkdirAll(
				filepath.Dir(entryPath),
				defaultDirectoryMode,
			); err != nil {
				return extractedSize, fmt.Errorf(
					"create parent directory for %q: %w",
					header.Name,
					err,
				)
			}

			written, err := extractRegularFile(
				tarReader,
				entryPath,
				header.Size,
			)
			if err != nil {
				return extractedSize, fmt.Errorf(
					"extract archive file %q: %w",
					header.Name,
					err,
				)
			}

			extractedSize += written

		case tar.TypeSymlink,
			tar.TypeLink,
			tar.TypeChar,
			tar.TypeBlock,
			tar.TypeFifo:

			return extractedSize, fmt.Errorf(
				"%w: %q",
				ErrUnsupportedEntry,
				header.Name,
			)

		default:
			return extractedSize, fmt.Errorf(
				"%w: %q uses type %d",
				ErrUnsupportedEntry,
				header.Name,
				header.Typeflag,
			)
		}
	}

	return extractedSize, nil
}

func extractRegularFile(
	reader io.Reader,
	destination string,
	expectedSize int64,
) (int64, error) {
	file, err := os.OpenFile(
		destination,
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
		defaultFileMode,
	)
	if err != nil {
		return 0, err
	}

	written, copyErr := io.CopyN(
		file,
		reader,
		expectedSize,
	)

	closeErr := file.Close()

	if copyErr != nil {
		_ = os.Remove(destination)
		return written, copyErr
	}

	if closeErr != nil {
		_ = os.Remove(destination)
		return written, closeErr
	}

	return written, nil
}

func secureArchiveDestination(
	destinationRoot string,
	entryName string,
) (string, error) {
	entryName = strings.ReplaceAll(
		entryName,
		"\\",
		"/",
	)

	entryName = strings.TrimPrefix(entryName, "/")
	entryName = filepath.Clean(entryName)

	if entryName == "." || entryName == "" {
		return destinationRoot, nil
	}

	if filepath.IsAbs(entryName) {
		return "", fmt.Errorf(
			"%w: %q",
			ErrUnsafeArchivePath,
			entryName,
		)
	}

	if entryName == ".." ||
		strings.HasPrefix(
			entryName,
			".."+string(os.PathSeparator),
		) {
		return "", fmt.Errorf(
			"%w: %q",
			ErrUnsafeArchivePath,
			entryName,
		)
	}

	destination := filepath.Join(
		destinationRoot,
		entryName,
	)

	cleanDestination := filepath.Clean(destination)

	if cleanDestination != destinationRoot &&
		!strings.HasPrefix(
			cleanDestination,
			destinationRoot+string(os.PathSeparator),
		) {
		return "", fmt.Errorf(
			"%w: %q",
			ErrUnsafeArchivePath,
			entryName,
		)
	}

	return cleanDestination, nil
}

func isSafeIdentifier(value string) bool {
	value = strings.TrimSpace(value)

	if value == "" || len(value) > 128 {
		return false
	}

	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z':
		case character >= 'A' && character <= 'Z':
		case character >= '0' && character <= '9':
		case character == '-':
		case character == '_':
		default:
			return false
		}
	}

	return true
}
