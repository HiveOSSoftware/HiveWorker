package network

import (
	"context"

	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

type Manager struct {
	client *client.Client
	name   string
}

func NewManager(client *client.Client, name string) *Manager {
	return &Manager{
		client: client,
		name:   name,
	}
}

func (m *Manager) Ensure(ctx context.Context) error {
	if m.name == "" {
		return nil
	}

	_, err := m.client.NetworkInspect(ctx, m.name, network.InspectOptions{})
	if err == nil {
		return nil
	}

	_, err = m.client.NetworkCreate(ctx, m.name, network.CreateOptions{
		Driver: "bridge",
		Labels: map[string]string{
			"managed_by": "hivepanel.worker",
		},
	})

	return err
}

func (m *Manager) Name() string {
	return m.name
}
