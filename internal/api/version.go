package api

import (
	"net/http"

	workerversion "hivepanel-worker/internal/version"
)

func (h *Handler) Version(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"version": workerversion.Version,
	})
}
