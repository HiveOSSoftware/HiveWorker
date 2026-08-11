package config

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"hivepanel-worker/internal/allocation"
)

type Config struct {
	ConfigPath string `yaml:"-"`

	Panel       PanelConfig      `yaml:"panel"`
	Worker      WorkerConfig     `yaml:"worker"`
	SFTP        SFTPConfig       `yaml:"sftp"`
	Node        NodeConfig       `yaml:"node"`
	Paths       Paths            `yaml:"paths"`
	Runtime     Runtime          `yaml:"runtime"`
	Docker      DockerConfig     `yaml:"docker"`
	Allocations AllocationConfig `yaml:"allocations"`
}

type PanelConfig struct {
	URL string `yaml:"url"`
}

type WorkerConfig struct {
	Token             string `yaml:"token"`
	RegistrationToken string `yaml:"registration_token"`
	Listen            string `yaml:"listen"`
}

type SFTPConfig struct {
	Enabled bool `yaml:"enabled"`

	// Local address the worker's SSH/SFTP server binds to.
	Listen string `yaml:"listen"`

	// Public connection details displayed to users by the panel.
	// These do not affect the local bind address.
	PublicFQDN string `yaml:"public_fqdn"`
	PublicPort int    `yaml:"public_port"`

	// Persistent SSH host private key used by the SFTP server.
	HostKeyPath string `yaml:"host_key_path"`

	// Timeout for authentication requests made to the panel.
	AuthTimeoutSeconds int `yaml:"auth_timeout_seconds"`
}

type NodeConfig struct {
	ID string `yaml:"id"`
}

type Paths struct {
	Data         string `yaml:"data"`
	Instances    string `yaml:"instances"`
	Backups      string `yaml:"backups"`
	BackupMounts string `yaml:"backup_mounts"`
}

type Runtime struct {
	Type string `yaml:"type"`
}

type DockerConfig struct {
	Network string `yaml:"network"`
}

type AllocationConfig struct {
	Entries []allocation.Allocation `yaml:"entries,omitempty"`

	// Legacy fields are retained so existing Worker configs can be loaded and
	// migrated automatically to exact allocation entries.
	IP        string   `yaml:"ip,omitempty"`
	IPs       []string `yaml:"ips,omitempty"`
	PortStart int      `yaml:"port_start,omitempty"`
	PortEnd   int      `yaml:"port_end,omitempty"`
}

type registrationRequest struct {
	RegistrationToken string `json:"registration_token"`
	Hostname          string `json:"hostname"`
	Platform          string `json:"platform"`
	Version           string `json:"version"`
}

type registrationResponse struct {
	NodeID string `json:"node_id"`
	Token  string `json:"token"`
}

func Load() Config {
	configPath := resolveConfigPath()

	cfg := Default()
	cfg.ConfigPath = configPath

	if data, err := os.ReadFile(configPath); err == nil {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			panic(fmt.Errorf("failed to parse config file: %w", err))
		}
	} else if !os.IsNotExist(err) {
		panic(fmt.Errorf("failed to read config file: %w", err))
	}

	applyEnvironmentOverrides(&cfg)
	normalise(&cfg)

	if cfg.Worker.RegistrationToken != "" && cfg.Worker.Token == "" {
		if err := RegisterWorker(&cfg); err != nil {
			panic(fmt.Errorf("failed to register worker: %w", err))
		}
	}

	if err := validate(cfg); err != nil {
		panic(fmt.Errorf("invalid worker configuration: %w", err))
	}

	return cfg
}

func Default() Config {
	return Config{
		Panel: PanelConfig{
			URL: "http://localhost:8000",
		},

		Worker: WorkerConfig{
			Token:  "super-secret-token",
			Listen: "0.0.0.0:8080",
		},

		SFTP: SFTPConfig{
			Enabled:            true,
			Listen:             "0.0.0.0:2022",
			PublicFQDN:         "",
			PublicPort:         2022,
			HostKeyPath:        defaultSFTPHostKeyPath(),
			AuthTimeoutSeconds: 10,
		},

		Node: NodeConfig{},

		Paths: Paths{
			Data:         "./data",
			Instances:    "./instances",
			Backups:      "./backups",
			BackupMounts: "./backup_mounts",
		},

		Runtime: Runtime{
			Type: "process",
		},

		Docker: DockerConfig{
			Network: "hivepanel",
		},

		Allocations: AllocationConfig{
			Entries:   []allocation.Allocation{},
			IP:        "0.0.0.0",
			IPs:       []string{},
			PortStart: 25565,
			PortEnd:   25600,
		},
	}
}

func RegisterWorker(cfg *Config) error {
	hostname, _ := os.Hostname()

	body, err := json.Marshal(registrationRequest{
		RegistrationToken: cfg.Worker.RegistrationToken,
		Hostname:          hostname,
		Platform:          runtime.GOOS + "/" + runtime.GOARCH,
		Version:           "dev",
	})
	if err != nil {
		return err
	}

	client := &http.Client{
		Timeout: 15 * time.Second,
	}

	request, err := http.NewRequest(
		http.MethodPost,
		trimSlash(cfg.Panel.URL)+"/api/worker/register",
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}

	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK ||
		response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf(
			"registration failed with HTTP %d",
			response.StatusCode,
		)
	}

	var registered registrationResponse
	if err := json.NewDecoder(response.Body).Decode(&registered); err != nil {
		return err
	}

	if registered.NodeID == "" {
		return fmt.Errorf("registration response did not include node_id")
	}

	if registered.Token == "" {
		return fmt.Errorf("registration response did not include token")
	}

	cfg.Node.ID = registered.NodeID
	cfg.Worker.Token = registered.Token
	cfg.Worker.RegistrationToken = ""

	return Save(*cfg)
}

func Save(cfg Config) error {
	if cfg.ConfigPath == "" {
		cfg.ConfigPath = resolveConfigPath()
	}

	normalise(&cfg)

	if err := validate(cfg); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(cfg.ConfigPath), 0755); err != nil {
		return err
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	return os.WriteFile(cfg.ConfigPath, data, 0600)
}

func applyEnvironmentOverrides(cfg *Config) {
	if token := os.Getenv("HIVEPANEL_WORKER_TOKEN"); token != "" {
		cfg.Worker.Token = token
	}

	if value := os.Getenv("HIVEPANEL_PANEL_URL"); value != "" {
		cfg.Panel.URL = value
	}

	if value := os.Getenv("HIVEPANEL_NODE_ID"); value != "" {
		cfg.Node.ID = value
	}

	if value := os.Getenv("HIVEPANEL_SFTP_LISTEN"); value != "" {
		cfg.SFTP.Listen = value
	}

	if value := os.Getenv("HIVEPANEL_SFTP_HOST_KEY"); value != "" {
		cfg.SFTP.HostKeyPath = value
	}

	if value := os.Getenv("HIVEPANEL_ALLOCATION_IP"); value != "" {
		cfg.Allocations.IP = value
		cfg.Allocations.Entries = nil
	}

	if value := os.Getenv("HIVEPANEL_ALLOCATION_IPS"); value != "" {
		cfg.Allocations.IPs = splitCommaSeparated(value)
		cfg.Allocations.Entries = nil
	}

	if value := os.Getenv("HIVEPANEL_ALLOCATION_PORT_START"); value != "" {
		var port int

		if _, err := fmt.Sscanf(value, "%d", &port); err == nil {
			cfg.Allocations.PortStart = port
			cfg.Allocations.Entries = nil
		}
	}

	if value := os.Getenv("HIVEPANEL_ALLOCATION_PORT_END"); value != "" {
		var port int

		if _, err := fmt.Sscanf(value, "%d", &port); err == nil {
			cfg.Allocations.PortEnd = port
			cfg.Allocations.Entries = nil
		}
	}
}

func normalise(cfg *Config) {
	cfg.Panel.URL = trimSlash(strings.TrimSpace(cfg.Panel.URL))
	cfg.Worker.Listen = strings.TrimSpace(cfg.Worker.Listen)

	cfg.Node.ID = strings.TrimSpace(cfg.Node.ID)

	cfg.SFTP.Listen = strings.TrimSpace(cfg.SFTP.Listen)
	cfg.SFTP.PublicFQDN = strings.TrimSpace(cfg.SFTP.PublicFQDN)
	cfg.SFTP.HostKeyPath = strings.TrimSpace(cfg.SFTP.HostKeyPath)

	cfg.Paths.Data = filepath.Clean(cfg.Paths.Data)
	cfg.Paths.Instances = filepath.Clean(cfg.Paths.Instances)
	cfg.Paths.Backups = filepath.Clean(cfg.Paths.Backups)
	cfg.Paths.BackupMounts = filepath.Clean(cfg.Paths.BackupMounts)

	cfg.Allocations.Entries = normaliseAllocationEntries(cfg.Allocations)

	// Once exact entries exist they become the persisted source of truth.
	cfg.Allocations.IP = ""
	cfg.Allocations.IPs = nil
	cfg.Allocations.PortStart = 0
	cfg.Allocations.PortEnd = 0

	if cfg.SFTP.PublicPort == 0 {
		cfg.SFTP.PublicPort = portFromListen(cfg.SFTP.Listen, 2022)
	}

	if cfg.SFTP.HostKeyPath == "" {
		cfg.SFTP.HostKeyPath = defaultSFTPHostKeyPath()
	}

	if cfg.SFTP.AuthTimeoutSeconds <= 0 {
		cfg.SFTP.AuthTimeoutSeconds = 10
	}
}

func validate(cfg Config) error {
	if cfg.Panel.URL == "" {
		return fmt.Errorf("panel.url is required")
	}

	if cfg.Worker.Listen == "" {
		return fmt.Errorf("worker.listen is required")
	}

	if cfg.Paths.Instances == "" || cfg.Paths.Instances == "." {
		return fmt.Errorf("paths.instances is required")
	}

	if len(cfg.Allocations.Entries) == 0 {
		return fmt.Errorf("at least one allocations.entries item is required")
	}

	seenAllocations := map[string]bool{}

	for _, item := range cfg.Allocations.Entries {
		ip := strings.TrimSpace(item.IP)

		if ip == "" {
			return fmt.Errorf("allocation IP is required")
		}

		if ip != "0.0.0.0" && ip != "::" && net.ParseIP(ip) == nil {
			return fmt.Errorf(
				"invalid allocation IP address: %s",
				ip,
			)
		}

		if item.Port < 1 || item.Port > 65535 {
			return fmt.Errorf(
				"allocation port must be between 1 and 65535 for %s",
				ip,
			)
		}

		key := net.JoinHostPort(ip, fmt.Sprintf("%d", item.Port))

		if seenAllocations[key] {
			return fmt.Errorf("duplicate allocation entry: %s", key)
		}

		seenAllocations[key] = true
	}

	if cfg.SFTP.Enabled {
		if cfg.SFTP.Listen == "" {
			return fmt.Errorf("sftp.listen is required when SFTP is enabled")
		}

		if cfg.SFTP.PublicPort < 1 || cfg.SFTP.PublicPort > 65535 {
			return fmt.Errorf("sftp.public_port must be between 1 and 65535")
		}

		if cfg.SFTP.HostKeyPath == "" {
			return fmt.Errorf("sftp.host_key_path is required")
		}

		if cfg.Worker.Token == "" {
			return fmt.Errorf(
				"worker.token is required when SFTP is enabled",
			)
		}

		if cfg.Node.ID == "" {
			return fmt.Errorf(
				"node.id is required when SFTP is enabled",
			)
		}
	}

	return nil
}

func normaliseAllocationEntries(cfg AllocationConfig) []allocation.Allocation {
	if len(cfg.Entries) > 0 {
		return deduplicateAllocationEntries(cfg.Entries)
	}

	ips := normaliseAllocationIPs(
		strings.TrimSpace(cfg.IP),
		cfg.IPs,
	)

	if len(ips) == 0 {
		return []allocation.Allocation{}
	}

	if cfg.PortStart < 1 || cfg.PortEnd < cfg.PortStart {
		return []allocation.Allocation{}
	}

	entries := make(
		[]allocation.Allocation,
		0,
		len(ips)*(cfg.PortEnd-cfg.PortStart+1),
	)

	for _, ip := range ips {
		for port := cfg.PortStart; port <= cfg.PortEnd; port++ {
			entries = append(entries, allocation.Allocation{
				IP:   ip,
				Port: port,
			})
		}
	}

	return deduplicateAllocationEntries(entries)
}

func deduplicateAllocationEntries(entries []allocation.Allocation) []allocation.Allocation {
	result := make([]allocation.Allocation, 0, len(entries))
	seen := map[string]bool{}

	for _, item := range entries {
		item.IP = strings.TrimSpace(item.IP)

		if item.IP == "" || item.Port < 1 || item.Port > 65535 {
			continue
		}

		key := net.JoinHostPort(
			item.IP,
			fmt.Sprintf("%d", item.Port),
		)

		if seen[key] {
			continue
		}

		seen[key] = true
		result = append(result, item)
	}

	return result
}

func normaliseAllocationIPs(primary string, configured []string) []string {
	values := make([]string, 0, len(configured)+1)

	for _, ip := range configured {
		ip = strings.TrimSpace(ip)

		if ip != "" {
			values = append(values, ip)
		}
	}

	primary = strings.TrimSpace(primary)

	if len(values) == 0 && primary != "" {
		values = append(values, primary)
	}

	result := make([]string, 0, len(values))
	seen := map[string]bool{}

	for _, ip := range values {
		ip = strings.TrimSpace(ip)

		if ip == "" || seen[ip] {
			continue
		}

		seen[ip] = true
		result = append(result, ip)
	}

	return result
}

func splitCommaSeparated(value string) []string {
	parts := strings.Split(value, ",")

	result := make([]string, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)

		if part != "" {
			result = append(result, part)
		}
	}

	return result
}

func resolveConfigPath() string {
	path := flag.String(
		"config",
		defaultConfigPath(),
		"Path to worker config file",
	)

	flag.Parse()

	return *path
}

func defaultConfigPath() string {
	if runtime.GOOS == "windows" {
		return "config.yaml"
	}

	return "/etc/hivepanel/worker.yml"
}

func defaultSFTPHostKeyPath() string {
	if runtime.GOOS == "windows" {
		return "data/sftp_host_ed25519"
	}

	return "/etc/hivepanel/keys/sftp_host_ed25519"
}

func trimSlash(value string) string {
	return strings.TrimRight(value, "/")
}

func portFromListen(value string, fallback int) int {
	index := strings.LastIndex(value, ":")

	if index == -1 || index == len(value)-1 {
		return fallback
	}

	var port int

	if _, err := fmt.Sscanf(value[index+1:], "%d", &port); err != nil {
		return fallback
	}

	if port < 1 || port > 65535 {
		return fallback
	}

	return port
}
