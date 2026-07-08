package docker

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/docker/docker/client"

	imageManager "hivepanel-worker/internal/image"
	networkManager "hivepanel-worker/internal/network"
)

const labelManagedBy = "hivepanel.worker"

type DockerRuntime struct {
	mutex      sync.Mutex
	client     *client.Client
	images     *imageManager.Manager
	containers map[string]string
	startedAt  map[string]time.Time
	cancelLogs map[string]context.CancelFunc
	stdin      map[string]io.WriteCloser
	networks   *networkManager.Manager
}

func New(networkName string) (*DockerRuntime, error) {
	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, err
	}

	networks := networkManager.NewManager(cli, networkName)

	if err := networks.Ensure(context.Background()); err != nil {
		return nil, err
	}

	return &DockerRuntime{
		client:     cli,
		images:     imageManager.NewManager(cli),
		containers: map[string]string{},
		startedAt:  map[string]time.Time{},
		cancelLogs: map[string]context.CancelFunc{},
		stdin:      map[string]io.WriteCloser{},
		networks:   networks,
	}, nil
}
