package api

import (
	"encoding/json"
	"net/http"

	"hivepanel-worker/internal/config"
)

type AllocationConfigurationRequest struct {
	IPs       []string `json:"ips"`
	PortStart int      `json:"port_start"`
	PortEnd   int      `json:"port_end"`
}

func (h *Handler) UpdateAllocationConfiguration(w http.ResponseWriter, r *http.Request) {
	var request AllocationConfigurationRequest

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid json body: "+err.Error(), http.StatusBadRequest)
		return
	}

	oldIPs, oldPortStart, oldPortEnd := h.Manager.AllocationConfiguration()

	normalisedIPs, err := h.Manager.ReconfigureAllocations(
		request.IPs,
		request.PortStart,
		request.PortEnd,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	oldConfig := h.Config

	h.Config.Allocations = config.AllocationConfig{
		IP:        normalisedIPs[0],
		IPs:       normalisedIPs,
		PortStart: request.PortStart,
		PortEnd:   request.PortEnd,
	}

	if err := config.Save(h.Config); err != nil {
		h.Config = oldConfig

		_, rollbackErr := h.Manager.ReconfigureAllocations(
			oldIPs,
			oldPortStart,
			oldPortEnd,
		)
		if rollbackErr != nil {
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
		"message":    "allocation configuration updated",
		"ips":        normalisedIPs,
		"port_start": request.PortStart,
		"port_end":   request.PortEnd,
	})
}
