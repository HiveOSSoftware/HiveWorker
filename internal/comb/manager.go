package comb

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type Comb struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Game        string            `json:"game"`
	Startup     string            `json:"startup"`
	Variables   map[string]string `json:"variables"`
	Install     []InstallStep     `json:"install"`
	Image       string            `json:"image"`
	WorkingDir  string            `json:"working_dir"`
	Entrypoint  []string          `json:"entrypoint"`
	Environment map[string]string `json:"environment"`
}

type InstallStep struct {
	ID   string         `json:"id,omitempty"`
	Type string         `json:"type"`
	With map[string]any `json:"with,omitempty"`
	Save string         `json:"save,omitempty"`
}

type Manager struct {
	combs map[string]*Comb
	dir   string
}

func NewManager(dataDir string) *Manager {
	return &Manager{
		combs: map[string]*Comb{},
		dir:   filepath.Join(dataDir, "combs"),
	}
}

func (m *Manager) Load() error {
	if err := os.MkdirAll(m.dir, 0755); err != nil {
		return err
	}

	files, err := os.ReadDir(m.dir)
	if err != nil {
		return err
	}

	for _, file := range files {
		if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(m.dir, file.Name()))
		if err != nil {
			continue
		}

		var comb Comb
		if err := json.Unmarshal(data, &comb); err != nil {
			continue
		}

		if comb.ID != "" {
			m.combs[comb.ID] = &comb
		}
	}

	return nil
}

func (m *Manager) List() []*Comb {
	list := make([]*Comb, 0, len(m.combs))

	for _, comb := range m.combs {
		list = append(list, comb)
	}

	return list
}

func (m *Manager) Get(id string) (*Comb, bool) {
	comb, exists := m.combs[id]
	return comb, exists
}

func (m *Manager) Require(id string) (*Comb, error) {
	comb, exists := m.Get(id)
	if !exists {
		return nil, errors.New("comb not found")
	}

	return comb, nil
}

func FromMap(data map[string]any) (*Comb, error) {
	bytes, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	var c Comb
	if err := json.Unmarshal(bytes, &c); err != nil {
		return nil, err
	}

	if c.ID == "" {
		return nil, errors.New("comb id is required")
	}

	return &c, nil
}
