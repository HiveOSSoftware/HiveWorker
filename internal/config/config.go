package config

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ConfigPath string `yaml:"-"`

	Panel       PanelConfig      `yaml:"panel"`
	Worker      WorkerConfig     `yaml:"worker"`
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

type NodeConfig struct {
	ID string `yaml:"id"`
}

type Paths struct {
	Data      string `yaml:"data"`
	Instances string `yaml:"instances"`
	Backups   string `yaml:"backups"`
}

type Runtime struct {
	Type string `yaml:"type"`
}

type DockerConfig struct {
	Network string `yaml:"network"`
}

type AllocationConfig struct {
	IP        string `yaml:"ip"`
	PortStart int    `yaml:"port_start"`
	PortEnd   int    `yaml:"port_end"`
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
	}

	if token := os.Getenv("HIVEPANEL_WORKER_TOKEN"); token != "" {
		cfg.Worker.Token = token
	}

	if cfg.Worker.RegistrationToken != "" && cfg.Worker.Token == "" {
		if err := RegisterWorker(&cfg); err != nil {
			panic(fmt.Errorf("failed to register worker: %w", err))
		}
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
		Node: NodeConfig{},
		Paths: Paths{
			Data:      "./data",
			Instances: "./instances",
			Backups:   "./backups",
		},
		Runtime: Runtime{
			Type: "process",
		},
		Docker: DockerConfig{
			Network: "hivepanel",
		},
		Allocations: AllocationConfig{
			IP:        "0.0.0.0",
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

	response, err := http.Post(
		trimSlash(cfg.Panel.URL)+"/api/worker/register",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("registration failed with HTTP %d", response.StatusCode)
	}

	var registered registrationResponse
	if err := json.NewDecoder(response.Body).Decode(&registered); err != nil {
		return err
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

	if err := os.MkdirAll(filepath.Dir(cfg.ConfigPath), 0755); err != nil {
		return err
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	return os.WriteFile(cfg.ConfigPath, data, 0600)
}

func resolveConfigPath() string {
	path := flag.String("config", defaultConfigPath(), "Path to worker config file")
	flag.Parse()

	return *path
}

func defaultConfigPath() string {
	if runtime.GOOS == "windows" {
		return "config.yaml"
	}

	return "/etc/hivepanel/worker.yml"
}

func trimSlash(value string) string {
	for len(value) > 0 && value[len(value)-1] == '/' {
		value = value[:len(value)-1]
	}

	return value
}
