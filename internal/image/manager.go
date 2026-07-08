package image

import (
	"context"
	"io"

	dockerimage "github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
)

type Manager struct {
	client *client.Client
}

func NewManager(client *client.Client) *Manager {
	return &Manager{
		client: client,
	}
}

func (m *Manager) Exists(ctx context.Context, image string) bool {
	_, _, err := m.client.ImageInspectWithRaw(ctx, image)
	return err == nil
}

func (m *Manager) Ensure(ctx context.Context, image string, log func(line string)) error {
	if image == "" {
		return nil
	}

	if m.Exists(ctx, image) {
		log("Docker image already exists: " + image)
		return nil
	}

	log("Pulling Docker image: " + image)

	reader, err := m.client.ImagePull(ctx, image, dockerimage.PullOptions{})
	if err != nil {
		return err
	}
	defer reader.Close()

	_, _ = io.Copy(io.Discard, reader)

	log("Docker image ready: " + image)

	return nil
}
