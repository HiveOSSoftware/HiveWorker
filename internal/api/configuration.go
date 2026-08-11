package api

import (
	"encoding/json"
	"net/http"

	"hivepanel-worker/internal/allocation"
	"hivepanel-worker/internal/config"
)

type AllocationConfigurationRequest struct {
	Allocations []allocation.Allocation `json:"allocations"`
}

func (h *Handler) UpdateAllocationConfiguration(w http.ResponseWriter, r *http.Request) {
	var request AllocationConfigurationRequest

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid json body: "+err.Error(), http.StatusBadRequest)
		return
	}

	oldAllocations := h.Manager.AllocationConfiguration()

	normalisedAllocations, err := h.Manager.ReconfigureAllocations(
		request.Allocations,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	oldConfig := h.Config

	h.Config.Allocations = config.AllocationConfig{
		Entries: normalisedAllocations,
	}

	if err := config.Save(h.Config); err != nil {
		h.Config = oldConfig

		if _, rollbackErr := h.Manager.ReconfigureAllocations(oldAllocations); rollbackErr != nil {
			http.Error(
				w,
				"failed to save worker configuration and failed to restore the previous live allocation configuration",
				http.StatusInternalServerError,
			)
			return
		}

		http.Error(
			w,
			"failed to save worker configuration: "+err.Error(),
			http.StatusInternalServerError,
		)
		return
	}

	writeJSON(w, map[string]any{
		"message":     "allocation configuration updated",
		"allocations": normalisedAllocations,
	})
}
