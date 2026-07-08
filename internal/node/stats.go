package node

import (
	"math"
	"path/filepath"
	"runtime"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

type Stats struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Platform string `json:"platform"`
	Kernel   string `json:"kernel"`
	Arch     string `json:"arch"`
	Uptime   uint64 `json:"uptime"`

	Host  HostStats  `json:"host"`
	Cells CellsStats `json:"cells"`
}

type HostStats struct {
	CPU    CPUStats    `json:"cpu"`
	Memory MemoryStats `json:"memory"`
	Disk   DiskStats   `json:"disk"`
}

type CPUStats struct {
	Used  float64 `json:"used"`
	Max   float64 `json:"max"`
	Model string  `json:"model"`
}

type MemoryStats struct {
	UsedGB float64 `json:"used_gb"`
	MaxGB  float64 `json:"max_gb"`
}

type DiskStats struct {
	UsedGB float64 `json:"used_gb"`
	MaxGB  float64 `json:"max_gb"`
	Path   string  `json:"path"`
}

type CellsStats struct {
	CPUUsed      float64 `json:"cpu_used"`
	MemoryUsedGB float64 `json:"memory_used_gb"`
	DiskUsedGB   float64 `json:"disk_used_gb"`
	Total        int     `json:"total"`
	Running      int     `json:"running"`
}

func GetStats(rootPath string) (*Stats, error) {
	hostInfo, _ := host.Info()
	virtualMemory, _ := mem.VirtualMemory()

	cpuPercent, _ := cpu.Percent(0, false)
	cpuInfo, _ := cpu.Info()
	cpuCount, _ := cpu.Counts(true)

	diskPath := normaliseDiskPath(rootPath)
	diskUsage, _ := disk.Usage(diskPath)

	cpuUsed := 0.0
	if len(cpuPercent) > 0 {
		cpuUsed = round(cpuPercent[0])
	}

	cpuModel := ""
	if len(cpuInfo) > 0 {
		cpuModel = cpuInfo[0].ModelName
	}

	return &Stats{
		Hostname: hostInfo.Hostname,
		OS:       runtime.GOOS,
		Platform: hostInfo.Platform,
		Kernel:   hostInfo.KernelVersion,
		Arch:     runtime.GOARCH,
		Uptime:   hostInfo.Uptime,
		Host: HostStats{
			CPU: CPUStats{
				Used:  cpuUsed,
				Max:   float64(cpuCount),
				Model: cpuModel,
			},
			Memory: MemoryStats{
				UsedGB: bytesToGB(virtualMemory.Used),
				MaxGB:  bytesToGB(virtualMemory.Total),
			},
			Disk: DiskStats{
				UsedGB: bytesToGB(diskUsage.Used),
				MaxGB:  bytesToGB(diskUsage.Total),
				Path:   diskPath,
			},
		},
		Cells: CellsStats{},
	}, nil
}

func normaliseDiskPath(path string) string {
	if path == "" || path == "." {
		abs, err := filepath.Abs(".")
		if err == nil {
			return abs
		}

		return "."
	}

	abs, err := filepath.Abs(path)
	if err == nil {
		return abs
	}

	return path
}

func bytesToGB(value uint64) float64 {
	return round(float64(value) / 1024 / 1024 / 1024)
}

func round(value float64) float64 {
	return math.Round(value*100) / 100
}
