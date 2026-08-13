package importer

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type LocalConfig struct {
	SourcePath string
	LocalPath  string
	Options    Options
}

type localFile struct {
	Path string
	Size int64
	Mode os.FileMode
}

func TestLocal(cfg LocalConfig) error {
	sourcePath, _, err := resolveLocalPaths(cfg)
	if err != nil {
		return err
	}

	info, err := os.Stat(sourcePath)
	if err != nil {
		return fmt.Errorf("unable to access local source path %s: %w", sourcePath, err)
	}

	if !info.IsDir() {
		return fmt.Errorf("local source path %s is not a directory", sourcePath)
	}

	return nil
}

func ImportLocal(
	cfg LocalConfig,
	progress func(stage string, percent int, message string),
) error {
	sourcePath, destinationPath, err := resolveLocalPaths(cfg)
	if err != nil {
		return err
	}

	if progress != nil {
		progress("Scanning", 15, "Scanning local Pterodactyl server files...")
	}

	files, err := collectLocalFiles(sourcePath, cfg.Options)
	if err != nil {
		return err
	}

	if cfg.Options.WipeBeforeImport {
		if progress != nil {
			progress("Preparing", 20, "Removing existing destination files...")
		}

		if err := wipeLocalDirectory(destinationPath); err != nil {
			return err
		}
	}

	if err := os.MkdirAll(destinationPath, 0755); err != nil {
		return err
	}

	totalBytes := int64(0)
	for _, file := range files {
		totalBytes += file.Size
	}

	var copiedBytes int64

	for index, file := range files {
		relative, err := filepath.Rel(sourcePath, file.Path)
		if err != nil {
			return err
		}

		destinationFile := filepath.Join(destinationPath, relative)

		if err := os.MkdirAll(filepath.Dir(destinationFile), 0755); err != nil {
			return err
		}

		if progress != nil {
			percent := transferPercent(
				index,
				len(files),
				copiedBytes,
				totalBytes,
			)

			progress(
				"Transferring",
				percent,
				"Copying "+filepath.ToSlash(relative),
			)
		}

		copied, err := copyLocalFile(
			file.Path,
			destinationFile,
			file.Mode,
		)
		if err != nil {
			return fmt.Errorf("copy %s: %w", filepath.ToSlash(relative), err)
		}

		copiedBytes += copied
	}

	if progress != nil {
		progress("Finalizing", 92, "Finalizing locally copied files...")
	}

	return nil
}

func resolveLocalPaths(cfg LocalConfig) (string, string, error) {
	sourcePath := strings.TrimSpace(cfg.SourcePath)
	destinationPath := strings.TrimSpace(cfg.LocalPath)

	if sourcePath == "" {
		return "", "", errors.New("local source path is required")
	}

	if destinationPath == "" {
		destinationPath = filepath.Join(sourcePath, ".hivepanel-validation-target")
	}

	sourcePath, err := filepath.Abs(sourcePath)
	if err != nil {
		return "", "", fmt.Errorf("resolve local source path: %w", err)
	}

	destinationPath, err = filepath.Abs(destinationPath)
	if err != nil {
		return "", "", fmt.Errorf("resolve local destination path: %w", err)
	}

	sourcePath = filepath.Clean(sourcePath)
	destinationPath = filepath.Clean(destinationPath)

	if sourcePath == destinationPath {
		return "", "", errors.New("local source and destination paths cannot be the same")
	}

	if cfg.LocalPath != "" {
		if pathContains(sourcePath, destinationPath) {
			return "", "", errors.New("local destination path cannot be inside the source path")
		}

		if pathContains(destinationPath, sourcePath) {
			return "", "", errors.New("local source path cannot be inside the destination path")
		}
	}

	return sourcePath, destinationPath, nil
}

func pathContains(parent string, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}

	return relative != "." &&
		relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func collectLocalFiles(
	sourceRoot string,
	options Options,
) ([]localFile, error) {
	result := []localFile{}

	err := filepath.WalkDir(
		sourceRoot,
		func(filePath string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}

			if filePath == sourceRoot || entry.IsDir() {
				return nil
			}

			info, err := entry.Info()
			if err != nil {
				return err
			}

			if !info.Mode().IsRegular() {
				return nil
			}

			relative, err := filepath.Rel(sourceRoot, filePath)
			if err != nil {
				return err
			}

			if !shouldImport(
				filepath.ToSlash(relative),
				options,
			) {
				return nil
			}

			result = append(result, localFile{
				Path: filePath,
				Size: info.Size(),
				Mode: info.Mode(),
			})

			return nil
		},
	)
	if err != nil {
		return nil, err
	}

	return result, nil
}

func copyLocalFile(
	sourcePath string,
	destinationPath string,
	mode os.FileMode,
) (int64, error) {
	source, err := os.Open(sourcePath)
	if err != nil {
		return 0, err
	}
	defer source.Close()

	destination, err := os.OpenFile(
		destinationPath,
		os.O_CREATE|os.O_TRUNC|os.O_WRONLY,
		mode.Perm(),
	)
	if err != nil {
		return 0, err
	}

	copied, copyErr := io.Copy(destination, source)
	closeErr := destination.Close()

	if copyErr != nil {
		return copied, copyErr
	}

	if closeErr != nil {
		return copied, closeErr
	}

	return copied, nil
}
