package files

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type ArchiveCreateRequest struct {
	Paths       []string
	Destination string
	Format      string
}

type ArchiveExtractRequest struct {
	Path        string
	Destination string
	Overwrite   bool
}

func CreateArchive(root string, request ArchiveCreateRequest, diskLimitBytes int64) error {
	if len(request.Paths) == 0 {
		return errors.New("at least one source path is required")
	}

	destination, err := safePath(root, request.Destination)
	if err != nil {
		return err
	}

	if _, err := os.Stat(destination); err == nil {
		return errors.New("destination archive already exists")
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}

	sources := make([]string, 0, len(request.Paths))
	sourceBytes := int64(0)

	for _, relative := range request.Paths {
		source, err := safePath(root, relative)
		if err != nil {
			return err
		}

		if _, err := os.Lstat(source); err != nil {
			return fmt.Errorf("source %q is unavailable: %w", relative, err)
		}

		size, err := pathSize(source)
		if err != nil {
			return err
		}

		sourceBytes += size
		sources = append(sources, source)
	}

	if err := ensureDiskCapacity(root, diskLimitBytes, sourceBytes); err != nil {
		return err
	}

	format := strings.ToLower(strings.TrimSpace(request.Format))

	switch format {
	case "zip":
		return createZip(root, destination, sources)

	case "tar.gz", "tgz":
		return createTarGz(root, destination, sources)

	default:
		return fmt.Errorf("unsupported archive format %q", request.Format)
	}
}

func ExtractArchive(root string, request ArchiveExtractRequest, diskLimitBytes int64) error {
	archivePath, err := safePath(root, request.Path)
	if err != nil {
		return err
	}

	destination := strings.TrimSpace(request.Destination)
	if destination == "" {
		destination = filepath.ToSlash(filepath.Dir(request.Path))
		if destination == "." {
			destination = ""
		}
	}

	destinationPath, err := safePath(root, destination)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(destinationPath, 0755); err != nil {
		return err
	}

	expandedBytes, err := archiveExpandedBytes(archivePath, request.Path)
	if err != nil {
		return err
	}

	if err := ensureDiskCapacity(root, diskLimitBytes, expandedBytes); err != nil {
		return err
	}

	lower := strings.ToLower(request.Path)

	switch {
	case strings.HasSuffix(lower, ".zip"):
		return extractZip(destinationPath, archivePath, request.Overwrite)

	case strings.HasSuffix(lower, ".tar.gz"),
		strings.HasSuffix(lower, ".tgz"):
		return extractTarGz(destinationPath, archivePath, request.Overwrite)

	default:
		return errors.New("only .zip, .tar.gz and .tgz archives are supported")
	}
}

func ensureDiskCapacity(root string, limitBytes int64, additionalBytes int64) error {
	if limitBytes <= 0 {
		return nil
	}

	currentBytes, err := pathSize(root)
	if err != nil {
		return err
	}

	if additionalBytes > limitBytes-currentBytes {
		return fmt.Errorf(
			"operation would exceed the Cell disk limit: %d bytes available, %d bytes required",
			maxInt64(0, limitBytes-currentBytes),
			additionalBytes,
		)
	}

	return nil
}

func pathSize(path string) (int64, error) {
	var total int64

	err := filepath.Walk(path, func(current string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		if info.Mode().IsRegular() {
			total += info.Size()
		}

		return nil
	})

	return total, err
}

func archiveExpandedBytes(archivePath string, name string) (int64, error) {
	lower := strings.ToLower(name)

	switch {
	case strings.HasSuffix(lower, ".zip"):
		reader, err := zip.OpenReader(archivePath)
		if err != nil {
			return 0, err
		}
		defer reader.Close()

		var total int64

		for _, entry := range reader.File {
			if entry.Mode()&os.ModeSymlink != 0 {
				return 0, fmt.Errorf("archive contains unsupported symlink %q", entry.Name)
			}

			if !entry.FileInfo().IsDir() {
				total += int64(entry.UncompressedSize64)
			}
		}

		return total, nil

	case strings.HasSuffix(lower, ".tar.gz"),
		strings.HasSuffix(lower, ".tgz"):
		file, err := os.Open(archivePath)
		if err != nil {
			return 0, err
		}
		defer file.Close()

		gzipReader, err := gzip.NewReader(file)
		if err != nil {
			return 0, err
		}
		defer gzipReader.Close()

		reader := tar.NewReader(gzipReader)
		var total int64

		for {
			header, err := reader.Next()

			if errors.Is(err, io.EOF) {
				break
			}

			if err != nil {
				return 0, err
			}

			switch header.Typeflag {
			case tar.TypeDir:
			case tar.TypeReg, tar.TypeRegA:
				total += header.Size
			case tar.TypeSymlink, tar.TypeLink:
				return 0, fmt.Errorf("archive contains unsupported link %q", header.Name)
			default:
				return 0, fmt.Errorf("archive contains unsupported entry %q", header.Name)
			}
		}

		return total, nil

	default:
		return 0, errors.New("only .zip, .tar.gz and .tgz archives are supported")
	}
}

func maxInt64(a int64, b int64) int64 {
	if a > b {
		return a
	}

	return b
}

func safePath(root string, relative string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}

	relative = filepath.FromSlash(strings.TrimSpace(relative))

	if filepath.IsAbs(relative) {
		return "", errors.New("absolute paths are not allowed")
	}

	full := filepath.Clean(filepath.Join(rootAbs, relative))
	rel, err := filepath.Rel(rootAbs, full)
	if err != nil {
		return "", err
	}

	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", errors.New("path escapes the Cell root")
	}

	return full, nil
}

func createZip(root string, destination string, sources []string) error {
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	writer := zip.NewWriter(file)

	cleanup := func(writeErr error) error {
		closeErr := writer.Close()
		fileErr := file.Close()

		if writeErr != nil {
			_ = os.Remove(destination)
			return writeErr
		}

		if closeErr != nil {
			_ = os.Remove(destination)
			return closeErr
		}

		if fileErr != nil {
			_ = os.Remove(destination)
			return fileErr
		}

		return nil
	}

	for _, source := range sources {
		err := filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}

			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlinks are not supported in archives: %s", path)
			}

			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}

			name := filepath.ToSlash(relative)

			if info.IsDir() {
				name += "/"
			}

			header, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}

			header.Name = name
			header.Method = zip.Deflate

			entry, err := writer.CreateHeader(header)
			if err != nil {
				return err
			}

			if info.IsDir() {
				return nil
			}

			input, err := os.Open(path)
			if err != nil {
				return err
			}
			defer input.Close()

			_, err = io.Copy(entry, input)
			return err
		})

		if err != nil {
			return cleanup(err)
		}
	}

	return cleanup(nil)
}

func createTarGz(root string, destination string, sources []string) error {
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)

	cleanup := func(writeErr error) error {
		tarErr := tarWriter.Close()
		gzipErr := gzipWriter.Close()
		fileErr := file.Close()

		if writeErr != nil {
			_ = os.Remove(destination)
			return writeErr
		}

		for _, closeErr := range []error{tarErr, gzipErr, fileErr} {
			if closeErr != nil {
				_ = os.Remove(destination)
				return closeErr
			}
		}

		return nil
	}

	for _, source := range sources {
		err := filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}

			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlinks are not supported in archives: %s", path)
			}

			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}

			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}

			header.Name = filepath.ToSlash(relative)

			if err := tarWriter.WriteHeader(header); err != nil {
				return err
			}

			if info.IsDir() {
				return nil
			}

			input, err := os.Open(path)
			if err != nil {
				return err
			}
			defer input.Close()

			_, err = io.Copy(tarWriter, input)
			return err
		})

		if err != nil {
			return cleanup(err)
		}
	}

	return cleanup(nil)
}

func extractZip(destination string, archivePath string, overwrite bool) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer reader.Close()

	for _, entry := range reader.File {
		target, err := safeArchiveTarget(destination, entry.Name)
		if err != nil {
			return err
		}

		if entry.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive contains unsupported symlink %q", entry.Name)
		}

		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, entry.Mode().Perm()); err != nil {
				return err
			}
			continue
		}

		if err := prepareExtractTarget(target, overwrite); err != nil {
			return err
		}

		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}

		input, err := entry.Open()
		if err != nil {
			return err
		}

		output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, entry.Mode().Perm())
		if err != nil {
			input.Close()
			return err
		}

		_, copyErr := io.Copy(output, input)
		closeOutputErr := output.Close()
		closeInputErr := input.Close()

		if copyErr != nil {
			return copyErr
		}

		if closeOutputErr != nil {
			return closeOutputErr
		}

		if closeInputErr != nil {
			return closeInputErr
		}
	}

	return nil
}

func extractTarGz(destination string, archivePath string, overwrite bool) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzipReader.Close()

	reader := tar.NewReader(gzipReader)

	for {
		header, err := reader.Next()

		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return err
		}

		target, err := safeArchiveTarget(destination, header.Name)
		if err != nil {
			return err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode).Perm()); err != nil {
				return err
			}

		case tar.TypeReg, tar.TypeRegA:
			if err := prepareExtractTarget(target, overwrite); err != nil {
				return err
			}

			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}

			output, err := os.OpenFile(
				target,
				os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
				os.FileMode(header.Mode).Perm(),
			)
			if err != nil {
				return err
			}

			_, copyErr := io.Copy(output, reader)
			closeErr := output.Close()

			if copyErr != nil {
				return copyErr
			}

			if closeErr != nil {
				return closeErr
			}

		case tar.TypeSymlink, tar.TypeLink:
			return fmt.Errorf("archive contains unsupported link %q", header.Name)

		default:
			return fmt.Errorf("archive contains unsupported entry %q", header.Name)
		}
	}

	return nil
}

func safeArchiveTarget(destination string, name string) (string, error) {
	cleanName := filepath.Clean(filepath.FromSlash(name))

	if cleanName == "." || cleanName == "" {
		return "", errors.New("archive contains an invalid path")
	}

	if filepath.IsAbs(cleanName) {
		return "", errors.New("archive contains an absolute path")
	}

	target := filepath.Join(destination, cleanName)

	destinationAbs, err := filepath.Abs(destination)
	if err != nil {
		return "", err
	}

	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}

	relative, err := filepath.Rel(destinationAbs, targetAbs)
	if err != nil {
		return "", err
	}

	if relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry %q escapes the extraction directory", name)
	}

	return targetAbs, nil
}

func prepareExtractTarget(target string, overwrite bool) error {
	_, err := os.Lstat(target)

	if os.IsNotExist(err) {
		return nil
	}

	if err != nil {
		return err
	}

	if !overwrite {
		return fmt.Errorf("destination already exists: %s", filepath.Base(target))
	}

	info, err := os.Lstat(target)
	if err != nil {
		return err
	}

	if info.IsDir() {
		return fmt.Errorf("cannot overwrite directory with file: %s", filepath.Base(target))
	}

	return os.Remove(target)
}
