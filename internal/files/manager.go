package files

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	recycleBinName = ".recycle_bin"

	DefaultPageSize = 250
	MaxPageSize     = 500
)

type FileEntry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	IsDir      bool   `json:"is_dir"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at,omitempty"`
}

type Pagination struct {
	Page       int `json:"page"`
	PerPage    int `json:"per_page"`
	Total      int `json:"total"`
	TotalPages int `json:"total_pages"`
	From       int `json:"from"`
	To         int `json:"to"`
}

type ListResponse struct {
	Path       string      `json:"path"`
	Files      []FileEntry `json:"files"`
	Pagination Pagination  `json:"pagination"`
}

func SafePath(baseDir string, requestedPath string) (string, error) {
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}

	cleanPath := filepath.Clean(requestedPath)

	if cleanPath == "." {
		cleanPath = ""
	}

	fullPath := filepath.Join(absBase, cleanPath)

	absFull, err := filepath.Abs(fullPath)
	if err != nil {
		return "", err
	}

	/*
		filepath.Rel is safer than checking strings.HasPrefix.

		For example, a prefix check could incorrectly consider:

			/srv/cells/example-two

		to be inside:

			/srv/cells/example
	*/
	relativePath, err := filepath.Rel(absBase, absFull)
	if err != nil {
		return "", err
	}

	if relativePath == ".." ||
		strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
		return "", errors.New("invalid path")
	}

	return absFull, nil
}

func List(
	baseDir string,
	requestedPath string,
	page int,
	perPage int,
) (*ListResponse, error) {
	if page < 1 {
		page = 1
	}

	if perPage < 1 {
		perPage = DefaultPageSize
	}

	if perPage > MaxPageSize {
		perPage = MaxPageSize
	}

	fullPath, err := SafePath(baseDir, requestedPath)
	if err != nil {
		return nil, err
	}

	directoryInfo, err := os.Stat(fullPath)
	if err != nil {
		return nil, err
	}

	if !directoryInfo.IsDir() {
		return nil, errors.New("path is not a directory")
	}

	directoryEntries, err := os.ReadDir(fullPath)
	if err != nil {
		return nil, err
	}

	/*
		Sorting must happen before pagination so that page results remain
		stable between requests.

		Directories are displayed before regular files, then entries are
		sorted alphabetically without case sensitivity.
	*/
	sort.SliceStable(directoryEntries, func(i int, j int) bool {
		left := directoryEntries[i]
		right := directoryEntries[j]

		if left.IsDir() != right.IsDir() {
			return left.IsDir()
		}

		leftName := strings.ToLower(left.Name())
		rightName := strings.ToLower(right.Name())

		if leftName == rightName {
			return left.Name() < right.Name()
		}

		return leftName < rightName
	})

	total := len(directoryEntries)

	totalPages := 1

	if total > 0 {
		totalPages = (total + perPage - 1) / perPage
	}

	if page > totalPages {
		page = totalPages
	}

	start := (page - 1) * perPage
	end := start + perPage

	if start > total {
		start = total
	}

	if end > total {
		end = total
	}

	result := make([]FileEntry, 0, end-start)

	for _, entry := range directoryEntries[start:end] {
		info, err := entry.Info()
		if err != nil {
			/*
				The entry may have been removed between os.ReadDir and
				entry.Info. Skip it rather than failing the entire list.
			*/
			continue
		}

		relativePath := filepath.Join(
			requestedPath,
			entry.Name(),
		)

		result = append(result, FileEntry{
			Name:       entry.Name(),
			Path:       filepath.ToSlash(relativePath),
			IsDir:      entry.IsDir(),
			Size:       info.Size(),
			ModifiedAt: info.ModTime().UTC().Format(time.RFC3339),
		})
	}

	from := 0
	to := 0

	if total > 0 {
		from = start + 1
		to = end
	}

	cleanRequestedPath := filepath.ToSlash(
		filepath.Clean(requestedPath),
	)

	if cleanRequestedPath == "." {
		cleanRequestedPath = ""
	}

	return &ListResponse{
		Path:  cleanRequestedPath,
		Files: result,
		Pagination: Pagination{
			Page:       page,
			PerPage:    perPage,
			Total:      total,
			TotalPages: totalPages,
			From:       from,
			To:         to,
		},
	}, nil
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

	return os.WriteFile(
		fullPath,
		[]byte(content),
		0644,
	)
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

func Rename(
	baseDir string,
	oldPath string,
	newPath string,
) error {
	oldFullPath, err := SafePath(baseDir, oldPath)
	if err != nil {
		return err
	}

	newFullPath, err := SafePath(baseDir, newPath)
	if err != nil {
		return err
	}

	if err := EnsureParentDir(newFullPath); err != nil {
		return err
	}

	return os.Rename(
		oldFullPath,
		newFullPath,
	)
}

func EnsureParentDir(path string) error {
	parent := filepath.Dir(path)

	return os.MkdirAll(parent, 0755)
}

func WriteBytes(
	baseDir string,
	requestedPath string,
	data []byte,
) error {
	fullPath, err := SafePath(baseDir, requestedPath)
	if err != nil {
		return err
	}

	if err := EnsureParentDir(fullPath); err != nil {
		return err
	}

	return os.WriteFile(
		fullPath,
		data,
		0644,
	)
}

func DownloadPath(
	baseDir string,
	requestedPath string,
) (string, error) {
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

	if cleanPath == recycleBinName ||
		strings.HasPrefix(cleanPath, recycleBinName+"/") {
		return errors.New(
			"cannot move recycle bin contents to recycle bin",
		)
	}

	source, err := SafePath(root, cleanPath)
	if err != nil {
		return err
	}

	if _, err := os.Stat(source); err != nil {
		return err
	}

	targetRelative := filepath.ToSlash(
		filepath.Join(
			recycleBinName,
			cleanPath,
		),
	)

	target, err := SafePath(root, targetRelative)
	if err != nil {
		return err
	}

	target = uniqueRecyclePath(target)

	if err := os.MkdirAll(
		filepath.Dir(target),
		0755,
	); err != nil {
		return err
	}

	return os.Rename(
		source,
		target,
	)
}

func RestoreFromRecycleBin(root string, path string) error {
	cleanPath, err := safeRelativePath(path)
	if err != nil {
		return err
	}

	if cleanPath != recycleBinName &&
		!strings.HasPrefix(cleanPath, recycleBinName+"/") {
		return errors.New("path is not inside recycle bin")
	}

	if cleanPath == recycleBinName {
		return errors.New("cannot restore recycle bin itself")
	}

	source, err := SafePath(root, cleanPath)
	if err != nil {
		return err
	}

	restoreRelative := strings.TrimPrefix(
		cleanPath,
		recycleBinName+"/",
	)

	target, err := SafePath(root, restoreRelative)
	if err != nil {
		return err
	}

	target = uniqueRecyclePath(target)

	if err := os.MkdirAll(
		filepath.Dir(target),
		0755,
	); err != nil {
		return err
	}

	return os.Rename(
		source,
		target,
	)
}

func PermanentDeleteFromRecycleBin(
	root string,
	path string,
) error {
	cleanPath, err := safeRelativePath(path)
	if err != nil {
		return err
	}

	if cleanPath != recycleBinName &&
		!strings.HasPrefix(cleanPath, recycleBinName+"/") {
		return errors.New(
			"permanent delete is only allowed inside recycle bin",
		)
	}

	if cleanPath == recycleBinName {
		return errors.New(
			"cannot permanently delete recycle bin itself",
		)
	}

	target, err := SafePath(root, cleanPath)
	if err != nil {
		return err
	}

	return os.RemoveAll(target)
}

func safeRelativePath(path string) (string, error) {
	clean := filepath.ToSlash(
		filepath.Clean(path),
	)

	if clean == "." {
		return "", errors.New("path is required")
	}

	clean = strings.TrimPrefix(clean, "/")

	if clean == "" || clean == "." {
		return "", errors.New("path is required")
	}

	if strings.HasPrefix(clean, "../") ||
		clean == ".." ||
		strings.Contains(clean, "/../") {
		return "", errors.New("invalid path")
	}

	return clean, nil
}

func uniqueRecyclePath(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}

	directory := filepath.Dir(path)
	extension := filepath.Ext(path)
	name := strings.TrimSuffix(
		filepath.Base(path),
		extension,
	)

	stamp := time.Now().Format(
		"2006-01-02 15-04-05",
	)

	candidate := filepath.Join(
		directory,
		name+" (deleted "+stamp+")"+extension,
	)

	if _, err := os.Stat(candidate); os.IsNotExist(err) {
		return candidate
	}

	for index := 2; ; index++ {
		candidate = filepath.Join(
			directory,
			name+
				" (deleted "+
				stamp+
				") "+
				strconv.Itoa(index)+
				extension,
		)

		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}

func CreateFile(
	baseDir string,
	requestedPath string,
) error {
	fullPath, err := SafePath(baseDir, requestedPath)
	if err != nil {
		return err
	}

	if _, err := os.Stat(fullPath); err == nil {
		return errors.New("file already exists")
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := EnsureParentDir(fullPath); err != nil {
		return err
	}

	file, err := os.OpenFile(
		fullPath,
		os.O_CREATE|os.O_EXCL|os.O_WRONLY,
		0644,
	)

	if err != nil {
		return err
	}

	return file.Close()
}

func UploadFromURL(
	baseDir string,
	requestedPath string,
	url string,
) error {
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

	response, err := client.Get(url)
	if err != nil {
		return err
	}

	defer response.Body.Close()

	if response.StatusCode < 200 ||
		response.StatusCode >= 300 {
		return errors.New("failed to download url")
	}

	output, err := os.OpenFile(
		fullPath,
		os.O_CREATE|os.O_TRUNC|os.O_WRONLY,
		0644,
	)

	if err != nil {
		return err
	}

	defer output.Close()

	_, err = io.Copy(
		output,
		response.Body,
	)

	return err
}
