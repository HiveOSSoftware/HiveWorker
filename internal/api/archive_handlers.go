package api

import (
	"encoding/json"
	"net/http"

	"hivepanel-worker/internal/files"
)

type ArchiveCreateRequest struct {
	Paths       []string `json:"paths"`
	Destination string   `json:"destination"`
	Format      string   `json:"format"`
}

type ArchiveExtractRequest struct {
	Path        string `json:"path"`
	Destination string `json:"destination"`
	Overwrite   bool   `json:"overwrite"`
}

func (h *Handler) CreateFileArchive(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if invalidCellID(id) {
		http.Error(w, "invalid cell id", http.StatusBadRequest)
		return
	}

	if !h.ensureFilesAllowed(w, id) {
		return
	}

	var request ArchiveCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}

	gameServer, exists := h.Manager.Get(id)
	if !exists {
		http.Error(w, "cell not found", http.StatusNotFound)
		return
	}

	diskLimitBytes := int64(gameServer.Limits.DiskMB) * 1024 * 1024

	err := files.CreateArchive(
		gameServer.Dir,
		files.ArchiveCreateRequest{
			Paths:       request.Paths,
			Destination: request.Destination,
			Format:      request.Format,
		},
		diskLimitBytes,
	)

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, map[string]any{
		"message":     "archive created",
		"destination": request.Destination,
	})
}

func (h *Handler) ExtractFileArchive(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if invalidCellID(id) {
		http.Error(w, "invalid cell id", http.StatusBadRequest)
		return
	}

	if !h.ensureFilesAllowed(w, id) {
		return
	}

	var request ArchiveExtractRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}

	gameServer, exists := h.Manager.Get(id)
	if !exists {
		http.Error(w, "cell not found", http.StatusNotFound)
		return
	}

	diskLimitBytes := int64(gameServer.Limits.DiskMB) * 1024 * 1024

	err := files.ExtractArchive(
		gameServer.Dir,
		files.ArchiveExtractRequest{
			Path:        request.Path,
			Destination: request.Destination,
			Overwrite:   request.Overwrite,
		},
		diskLimitBytes,
	)

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, map[string]any{
		"message":     "archive extracted",
		"path":        request.Path,
		"destination": request.Destination,
	})
}
