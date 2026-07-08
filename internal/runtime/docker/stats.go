package docker

import (
	"context"
	"encoding/json"
	"io"
	"time"

	hiveruntime "hivepanel-worker/internal/runtime"
)

func (r *DockerRuntime) Stats(id string) (*hiveruntime.Stats, error) {
	r.mutex.Lock()
	containerID, exists := r.containers[id]
	r.mutex.Unlock()

	if !exists || containerID == "" {
		return &hiveruntime.Stats{}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	statsResp, err := r.client.ContainerStatsOneShot(ctx, containerID)
	if err != nil {
		return &hiveruntime.Stats{}, nil
	}
	defer statsResp.Body.Close()

	return decodeDockerStats(statsResp.Body)
}

func decodeDockerStats(reader io.Reader) (*hiveruntime.Stats, error) {
	var stats struct {
		CPUStats struct {
			CPUUsage struct {
				TotalUsage uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			SystemCPUUsage uint64 `json:"system_cpu_usage"`
			OnlineCPUs     uint32 `json:"online_cpus"`
		} `json:"cpu_stats"`

		PreCPUStats struct {
			CPUUsage struct {
				TotalUsage uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			SystemCPUUsage uint64 `json:"system_cpu_usage"`
		} `json:"precpu_stats"`

		MemoryStats struct {
			Usage uint64 `json:"usage"`
			Stats struct {
				Cache uint64 `json:"cache"`
			} `json:"stats"`
		} `json:"memory_stats"`

		Networks map[string]struct {
			RxBytes uint64 `json:"rx_bytes"`
			TxBytes uint64 `json:"tx_bytes"`
		} `json:"networks"`
	}

	if err := json.NewDecoder(reader).Decode(&stats); err != nil {
		return &hiveruntime.Stats{}, err
	}

	memoryUsage := stats.MemoryStats.Usage
	if stats.MemoryStats.Stats.Cache > 0 && memoryUsage > stats.MemoryStats.Stats.Cache {
		memoryUsage -= stats.MemoryStats.Stats.Cache
	}

	cpuDelta := float64(stats.CPUStats.CPUUsage.TotalUsage - stats.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(stats.CPUStats.SystemCPUUsage - stats.PreCPUStats.SystemCPUUsage)

	onlineCPUs := float64(stats.CPUStats.OnlineCPUs)
	if onlineCPUs <= 0 {
		onlineCPUs = 1
	}

	cpuPercent := 0.0
	if systemDelta > 0 && cpuDelta > 0 {
		cpuPercent = (cpuDelta / systemDelta) * onlineCPUs * 100
	}

	var rxBytes uint64
	var txBytes uint64

	for _, networkStats := range stats.Networks {
		rxBytes += networkStats.RxBytes
		txBytes += networkStats.TxBytes
	}

	return &hiveruntime.Stats{
		CPU:            cpuPercent,
		MemoryMB:       float64(memoryUsage) / 1024 / 1024,
		NetworkRxBytes: rxBytes,
		NetworkTxBytes: txBytes,
	}, nil
}
