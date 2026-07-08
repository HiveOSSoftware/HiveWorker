package files

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type FileEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

const recycleBinName = ".recycle_bin"

func SafePath(baseDir string, requestedPath string) (string, error) {
	cleanPath := filepath.Clean(requestedPath)

	if cleanPath == "." {
		cleanPath = ""
	}

	fullPath := filepath.Join(baseDir, cleanPath)
	absBase, _ := filepath.Abs(baseDir)
	absFull, _ := filepath.Abs(fullPath)

	if !strings.HasPrefix(absFull, absBase) {
		return "", errors.New("invalid path")
	}

	return absFull, nil
}

func List(baseDir string, requestedPath string) ([]FileEntry, error) {
	fullPath, err := SafePath(baseDir, requestedPath)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		return nil, err
	}

	result := make([]FileEntry, 0)

	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}

		relativePath := filepath.Join(requestedPath, entry.Name())

		result = append(result, FileEntry{
			Name:  entry.Name(),
			Path:  filepath.ToSlash(relativePath),
			IsDir: entry.IsDir(),
			Size:  info.Size(),
		})
	}

	return result, nil
}

func Read(baseDir string, requestedPath string) (string, error) {
	fullPath, err := SafePath(baseDir, requestedPath)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

func Write(baseDir string, requestedPath string, content string) error {
	fullPath, err := SafePath(baseDir, requestedPath)
	if err != nil {
		return err
	}

	if err := EnsureParentDir(fullPath); err != nil {
		return err
	}

	return os.WriteFile(fullPath, []byte(content), 0644)
}

func Delete(baseDir string, requestedPath string) error {
	fullPath, err := SafePath(baseDir, requestedPath)
	if err != nil {
		return err
	}

	return os.RemoveAll(fullPath)
}

func CreateFolder(baseDir string, requestedPath string) error {
	fullPath, err := SafePath(baseDir, requestedPath)
	if err != nil {
		return err
	}

	return os.MkdirAll(fullPath, 0755)
}

func Rename(baseDir string, oldPath string, newPath string) error {
	oldFullPath, err := SafePath(baseDir, oldPath)
	if err != nil {
		return err
	}

	newFullPath, err := SafePath(baseDir, newPath)
	if err != nil {
		return err
	}

	return os.Rename(oldFullPath, newFullPath)
}

func EnsureParentDir(path string) error {
	parent := filepath.Dir(path)
	return os.MkdirAll(parent, 0755)
}

func WriteBytes(baseDir string, requestedPath string, data []byte) error {
	fullPath, err := SafePath(baseDir, requestedPath)
	if err != nil {
		return err
	}

	if err := EnsureParentDir(fullPath); err != nil {
		return err
	}

	return os.WriteFile(fullPath, data, 0644)
}

func DownloadPath(baseDir string, requestedPath string) (string, error) {
	fullPath, err := SafePath(baseDir, requestedPath)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		return "", err
	}

	if info.IsDir() {
		return "", errors.New("cannot download a folder")
	}

	return fullPath, nil
}

func MoveToRecycleBin(root string, path string) error {
	cleanPath, err := safeRelativePath(path)
	if err != nil {
		return err
	}

	if cleanPath == recycleBinName || strings.HasPrefix(cleanPath, recycleBinName+"/") {
		return errors.New("cannot move recycle bin contents to recycle bin")
	}

	source := filepath.Join(root, filepath.FromSlash(cleanPath))

	if _, err := os.Stat(source); err != nil {
		return err
	}

	targetRelative := filepath.ToSlash(filepath.Join(recycleBinName, cleanPath))
	target := filepath.Join(root, filepath.FromSlash(targetRelative))
	target = uniqueRecyclePath(target)

	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}

	return os.Rename(source, target)
}

func RestoreFromRecycleBin(root string, path string) error {
	cleanPath, err := safeRelativePath(path)
	if err != nil {
		return err
	}

	if cleanPath != recycleBinName && !strings.HasPrefix(cleanPath, recycleBinName+"/") {
		return errors.New("path is not inside recycle bin")
	}

	if cleanPath == recycleBinName {
		return errors.New("cannot restore recycle bin itself")
	}

	source := filepath.Join(root, filepath.FromSlash(cleanPath))

	restoreRelative := strings.TrimPrefix(cleanPath, recycleBinName+"/")
	target := filepath.Join(root, filepath.FromSlash(restoreRelative))
	target = uniqueRecyclePath(target)

	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}

	return os.Rename(source, target)
}

func PermanentDeleteFromRecycleBin(root string, path string) error {
	cleanPath, err := safeRelativePath(path)
	if err != nil {
		return err
	}

	if cleanPath != recycleBinName && !strings.HasPrefix(cleanPath, recycleBinName+"/") {
		return errors.New("permanent delete is only allowed inside recycle bin")
	}

	if cleanPath == recycleBinName {
		return errors.New("cannot permanently delete recycle bin itself")
	}

	target := filepath.Join(root, filepath.FromSlash(cleanPath))

	return os.RemoveAll(target)
}

func safeRelativePath(path string) (string, error) {
	clean := filepath.ToSlash(filepath.Clean(path))

	if clean == "." {
		return "", errors.New("path is required")
	}

	clean = strings.TrimPrefix(clean, "/")

	if clean == "" || clean == "." {
		return "", errors.New("path is required")
	}

	if strings.HasPrefix(clean, "../") || clean == ".." || strings.Contains(clean, "/../") {
		return "", errors.New("invalid path")
	}

	return clean, nil
}

func uniqueRecyclePath(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}

	dir := filepath.Dir(path)
	ext := filepath.Ext(path)
	name := strings.TrimSuffix(filepath.Base(path), ext)
	stamp := time.Now().Format("2006-01-02 15-04-05")

	candidate := filepath.Join(dir, name+" (deleted "+stamp+")"+ext)

	if _, err := os.Stat(candidate); os.IsNotExist(err) {
		return candidate
	}

	for i := 2; ; i++ {
		candidate = filepath.Join(dir, name+" (deleted "+stamp+") "+strconv.Itoa(i)+ext)

		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}

func CreateFile(baseDir string, requestedPath string) error {
	fullPath, err := SafePath(baseDir, requestedPath)
	if err != nil {
		return err
	}

	if _, err := os.Stat(fullPath); err == nil {
		return errors.New("file already exists")
	}

	if err := EnsureParentDir(fullPath); err != nil {
		return err
	}

	file, err := os.OpenFile(fullPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	return file.Close()
}

func UploadFromURL(baseDir string, requestedPath string, url string) error {
	if url == "" {
		return errors.New("url is required")
	}

	fullPath, err := SafePath(baseDir, requestedPath)
	if err != nil {
		return err
	}

	if err := EnsureParentDir(fullPath); err != nil {
		return err
	}

	client := &http.Client{
		Timeout: 10 * time.Minute,
	}

	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("failed to download url")
	}

	out, err := os.OpenFile(fullPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}
