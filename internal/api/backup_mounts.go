package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"hivepanel-worker/internal/backup"
	"hivepanel-worker/internal/files"
)

const (
	defaultBackupMountPageSize = 250
	maxBackupMountPageSize     = 500
)

type mountBackupRequest struct {
	MountID string `json:"mount_id"`
}

type mountBackupResponse struct {
	MountID       string `json:"mount_id"`
	Path          string `json:"path"`
	ExtractedSize int64  `json:"extracted_size"`
}

type unmountBackupResponse struct {
	MountID string `json:"mount_id"`
	Status  string `json:"status"`
}

// MountBackup extracts a backup into its temporary mount directory.
//
// POST /cells/{id}/backups/{name}/mount
//
// Request:
//
//	{
//	    "mount_id": "f9df053e-fd3e-48c3-9c2e-dd1af7e128c7"
//	}
func (handler *Handler) MountBackup(
	writer http.ResponseWriter,
	request *http.Request,
) {
	cellID := strings.TrimSpace(request.PathValue("id"))
	backupID := strings.TrimSpace(request.PathValue("name"))

	if cellID == "" {
		writeBackupMountError(
			writer,
			http.StatusBadRequest,
			"cell id is required",
		)

		return
	}

	if backupID == "" {
		writeBackupMountError(
			writer,
			http.StatusBadRequest,
			"backup id is required",
		)

		return
	}

	if handler.BackupMounts == nil {
		writeBackupMountError(
			writer,
			http.StatusInternalServerError,
			"backup mount service is unavailable",
		)

		return
	}

	var payload mountBackupRequest

	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&payload); err != nil {
		writeBackupMountError(
			writer,
			http.StatusBadRequest,
			"invalid request body",
		)

		return
	}

	payload.MountID = strings.TrimSpace(payload.MountID)

	if payload.MountID == "" {
		writeBackupMountError(
			writer,
			http.StatusBadRequest,
			"mount_id is required",
		)

		return
	}

	result, err := handler.BackupMounts.Mount(
		cellID,
		backupID,
		payload.MountID,
	)
	if err != nil {
		switch {
		case errors.Is(err, backup.ErrBackupNotFound):
			writeBackupMountError(
				writer,
				http.StatusNotFound,
				"backup archive was not found",
			)

		case errors.Is(err, backup.ErrMountAlreadyExists):
			writeBackupMountError(
				writer,
				http.StatusConflict,
				"backup mount already exists",
			)

		case errors.Is(err, backup.ErrInvalidBackupID):
			writeBackupMountError(
				writer,
				http.StatusBadRequest,
				"invalid backup id",
			)

		case errors.Is(err, backup.ErrInvalidMountID):
			writeBackupMountError(
				writer,
				http.StatusBadRequest,
				"invalid mount id",
			)

		case errors.Is(err, backup.ErrUnsafeArchivePath):
			writeBackupMountError(
				writer,
				http.StatusUnprocessableEntity,
				"backup contains an unsafe archive path",
			)

		case errors.Is(err, backup.ErrUnsupportedEntry):
			writeBackupMountError(
				writer,
				http.StatusUnprocessableEntity,
				"backup contains an unsupported archive entry",
			)

		default:
			writeBackupMountError(
				writer,
				http.StatusInternalServerError,
				"failed to mount backup: "+err.Error(),
			)
		}

		return
	}

	writeBackupMountJSON(
		writer,
		http.StatusCreated,
		mountBackupResponse{
			MountID:       payload.MountID,
			Path:          result.Path,
			ExtractedSize: result.ExtractedSize,
		},
	)
}

// UnmountBackup removes an extracted backup mount.
//
// DELETE /cells/{id}/backup-mounts/{mountId}
func (handler *Handler) UnmountBackup(
	writer http.ResponseWriter,
	request *http.Request,
) {
	cellID := strings.TrimSpace(request.PathValue("id"))
	mountID := strings.TrimSpace(request.PathValue("mountId"))

	if cellID == "" {
		writeBackupMountError(
			writer,
			http.StatusBadRequest,
			"cell id is required",
		)

		return
	}

	if mountID == "" {
		writeBackupMountError(
			writer,
			http.StatusBadRequest,
			"mount id is required",
		)

		return
	}

	if handler.BackupMounts == nil {
		writeBackupMountError(
			writer,
			http.StatusInternalServerError,
			"backup mount service is unavailable",
		)

		return
	}

	if err := handler.BackupMounts.Unmount(
		cellID,
		mountID,
	); err != nil {
		if errors.Is(err, backup.ErrInvalidMountID) {
			writeBackupMountError(
				writer,
				http.StatusBadRequest,
				"invalid mount id",
			)

			return
		}

		writeBackupMountError(
			writer,
			http.StatusInternalServerError,
			"failed to unmount backup: "+err.Error(),
		)

		return
	}

	writeBackupMountJSON(
		writer,
		http.StatusOK,
		unmountBackupResponse{
			MountID: mountID,
			Status:  "unmounted",
		},
	)
}

// ListMountedBackupFiles lists files from an extracted backup mount.
//
// GET /cells/{id}/backup-mounts/{mountId}/files
//
//	?path=plugins
//	&page=1
//	&per_page=250
func (handler *Handler) ListMountedBackupFiles(
	writer http.ResponseWriter,
	request *http.Request,
) {
	cellID := strings.TrimSpace(request.PathValue("id"))
	mountID := strings.TrimSpace(request.PathValue("mountId"))

	if cellID == "" {
		writeBackupMountError(
			writer,
			http.StatusBadRequest,
			"cell id is required",
		)

		return
	}

	if mountID == "" {
		writeBackupMountError(
			writer,
			http.StatusBadRequest,
			"mount id is required",
		)

		return
	}

	if handler.BackupMounts == nil {
		writeBackupMountError(
			writer,
			http.StatusInternalServerError,
			"backup mount service is unavailable",
		)

		return
	}

	mountPath, err := handler.BackupMounts.MountPath(
		cellID,
		mountID,
	)
	if err != nil {
		switch {
		case errors.Is(err, os.ErrNotExist):
			writeBackupMountError(
				writer,
				http.StatusNotFound,
				"backup mount was not found",
			)

		case errors.Is(err, backup.ErrInvalidMountID):
			writeBackupMountError(
				writer,
				http.StatusBadRequest,
				"invalid mount id",
			)

		default:
			writeBackupMountError(
				writer,
				http.StatusInternalServerError,
				"failed to resolve backup mount: "+err.Error(),
			)
		}

		return
	}

	page := parseBackupMountInteger(
		request.URL.Query().Get("page"),
		1,
	)

	perPage := parseBackupMountInteger(
		request.URL.Query().Get("per_page"),
		defaultBackupMountPageSize,
	)

	if page < 1 {
		page = 1
	}

	if perPage < 1 {
		perPage = defaultBackupMountPageSize
	}

	if perPage > maxBackupMountPageSize {
		perPage = maxBackupMountPageSize
	}

	path := strings.TrimSpace(
		request.URL.Query().Get("path"),
	)

	result, err := files.List(
		mountPath,
		path,
		page,
		perPage,
	)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeBackupMountError(
				writer,
				http.StatusNotFound,
				"backup directory was not found",
			)

			return
		}

		writeBackupMountError(
			writer,
			http.StatusBadRequest,
			"failed to list mounted backup files: "+err.Error(),
		)

		return
	}

	writeBackupMountJSON(
		writer,
		http.StatusOK,
		map[string]any{
			"mount_id":   mountID,
			"read_only":  true,
			"path":       result.Path,
			"files":      result.Files,
			"pagination": result.Pagination,
		},
	)
}

func parseBackupMountInteger(
	value string,
	fallback int,
) int {
	value = strings.TrimSpace(value)

	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}

	return parsed
}

func writeBackupMountError(
	writer http.ResponseWriter,
	status int,
	message string,
) {
	writeBackupMountJSON(
		writer,
		status,
		map[string]string{
			"error": message,
		},
	)
}

func writeBackupMountJSON(
	writer http.ResponseWriter,
	status int,
	value any,
) {
	writer.Header().Set(
		"Content-Type",
		"application/json",
	)

	writer.WriteHeader(status)

	if err := json.NewEncoder(writer).Encode(value); err != nil {
		return
	}
}
