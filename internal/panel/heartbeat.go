package panel

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"runtime"
	"time"

	"hivepanel-worker/internal/config"
	nodestats "hivepanel-worker/internal/node"
)

type HeartbeatPayload struct {
	Version  string `json:"version"`
	Hostname string `json:"hostname"`
	Platform string `json:"platform"`
	Stats    any    `json:"stats"`
}

func StartHeartbeat(cfg config.Config) {
	if cfg.Panel.URL == "" || cfg.Worker.Token == "" {
		log.Println("Panel heartbeat disabled: missing panel URL or worker token")
		return
	}

	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()

		sendHeartbeat(cfg)

		for range ticker.C {
			sendHeartbeat(cfg)
		}
	}()
}

func sendHeartbeat(cfg config.Config) {
	hostname, _ := os.Hostname()

	stats, err := nodestats.GetStats(cfg.Paths.Data)
	if err != nil {
		log.Println("Failed to collect node stats:", err)
		return
	}

	payload := HeartbeatPayload{
		Version:  "dev",
		Hostname: hostname,
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
		Stats:    stats,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Println("Failed to encode heartbeat:", err)
		return
	}

	request, err := http.NewRequest(
		http.MethodPost,
		trimSlash(cfg.Panel.URL)+"/api/worker/heartbeat",
		bytes.NewReader(body),
	)
	if err != nil {
		log.Println("Failed to build heartbeat request:", err)
		return
	}

	request.Header.Set("Authorization", "Bearer "+cfg.Worker.Token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	response, err := client.Do(request)
	if err != nil {
		log.Println("Failed to send heartbeat:", err)
		return
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		log.Println("Heartbeat failed with HTTP status:", response.StatusCode)
		return
	}
}

func trimSlash(value string) string {
	for len(value) > 0 && value[len(value)-1] == '/' {
		value = value[:len(value)-1]
	}

	return value
}
