package migrationdetect

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type RuntimeContainer struct {
	UUID      string            `json:"uuid"`
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	State     string            `json:"state"`
	Status    string            `json:"status"`
	MatchedBy []string          `json:"matched_by"`
	Labels    map[string]string `json:"labels,omitempty"`
}

type RuntimeCheck struct {
	Available         bool               `json:"available"`
	Engine            string             `json:"engine,omitempty"`
	AllStopped        *bool              `json:"all_stopped,omitempty"`
	MatchedCount      int                `json:"matched_count"`
	ActiveCount       int                `json:"active_count"`
	ActiveContainers  []RuntimeContainer `json:"active_containers"`
	MatchedContainers []RuntimeContainer `json:"matched_containers"`
	Error             string             `json:"error,omitempty"`
}

type dockerContainerSummary struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Image  string            `json:"Image"`
	State  string            `json:"State"`
	Status string            `json:"Status"`
	Labels map[string]string `json:"Labels"`
	Mounts []struct {
		Type        string `json:"Type"`
		Name        string `json:"Name"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
	} `json:"Mounts"`
}

func inspectSourceRuntime(uuids []string) RuntimeCheck {
	result := RuntimeCheck{
		Available:         false,
		Engine:            "docker",
		MatchedContainers: []RuntimeContainer{},
		ActiveContainers:  []RuntimeContainer{},
	}

	client, endpoint, err := dockerHTTPClient()
	if err != nil {
		result.Error = err.Error()
		return result
	}

	ctx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		endpoint+"/containers/json?all=1",
		nil,
	)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	response, err := client.Do(request)
	if err != nil {
		result.Error = fmt.Sprintf(
			"docker runtime could not be queried: %v",
			err,
		)
		return result
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		result.Error = fmt.Sprintf(
			"docker runtime returned HTTP %d",
			response.StatusCode,
		)
		return result
	}

	var containers []dockerContainerSummary
	if err := json.NewDecoder(response.Body).Decode(&containers); err != nil {
		result.Error = fmt.Sprintf(
			"docker runtime returned invalid JSON: %v",
			err,
		)
		return result
	}

	result.Available = true

	for _, container := range containers {
		for _, uuid := range uuids {
			evidence := containerMatchEvidence(
				container,
				uuid,
			)

			if len(evidence) == 0 {
				continue
			}

			item := RuntimeContainer{
				UUID:      uuid,
				ID:        shortContainerID(container.ID),
				Name:      containerDisplayName(container),
				State:     strings.ToLower(strings.TrimSpace(container.State)),
				Status:    container.Status,
				MatchedBy: evidence,
				Labels:    container.Labels,
			}

			result.MatchedContainers = append(
				result.MatchedContainers,
				item,
			)

			if sourceContainerActive(item.State) {
				result.ActiveContainers = append(
					result.ActiveContainers,
					item,
				)
			}

			break
		}
	}

	result.MatchedCount = len(result.MatchedContainers)
	result.ActiveCount = len(result.ActiveContainers)

	allStopped := result.ActiveCount == 0
	result.AllStopped = &allStopped

	return result
}

func dockerHTTPClient() (*http.Client, string, error) {
	dockerHost := strings.TrimSpace(
		os.Getenv("DOCKER_HOST"),
	)

	if dockerHost == "" {
		dockerHost = "unix:///var/run/docker.sock"
	}

	if strings.HasPrefix(dockerHost, "unix://") {
		socketPath := strings.TrimPrefix(
			dockerHost,
			"unix://",
		)

		socketPath = filepath.Clean(socketPath)

		if _, err := os.Stat(socketPath); err != nil {
			return nil, "", fmt.Errorf(
				"docker socket is unavailable at %s",
				socketPath,
			)
		}

		transport := &http.Transport{
			DialContext: func(
				ctx context.Context,
				network string,
				address string,
			) (net.Conn, error) {
				dialer := &net.Dialer{
					Timeout: 5 * time.Second,
				}

				return dialer.DialContext(
					ctx,
					"unix",
					socketPath,
				)
			},
		}

		return &http.Client{
			Transport: transport,
			Timeout:   5 * time.Second,
		}, "http://docker", nil
	}

	parsed, err := url.Parse(dockerHost)
	if err != nil {
		return nil, "", fmt.Errorf(
			"invalid DOCKER_HOST: %w",
			err,
		)
	}

	switch parsed.Scheme {
	case "tcp":
		parsed.Scheme = "http"
	case "http", "https":
	default:
		return nil, "", fmt.Errorf(
			"unsupported DOCKER_HOST scheme %q",
			parsed.Scheme,
		)
	}

	return &http.Client{
		Timeout: 5 * time.Second,
	}, strings.TrimRight(parsed.String(), "/"), nil
}

func containerMatchEvidence(
	container dockerContainerSummary,
	uuid string,
) []string {
	uuid = strings.ToLower(
		strings.TrimSpace(uuid),
	)

	if uuid == "" {
		return nil
	}

	evidence := []string{}

	for _, name := range container.Names {
		if strings.Contains(
			strings.ToLower(name),
			uuid,
		) {
			evidence = append(
				evidence,
				"name",
			)
			break
		}
	}

	for key, value := range container.Labels {
		keyLower := strings.ToLower(key)
		valueLower := strings.ToLower(value)

		if strings.Contains(keyLower, uuid) ||
			strings.Contains(valueLower, uuid) {
			evidence = append(
				evidence,
				"label",
			)
			break
		}
	}

	for _, mount := range container.Mounts {
		if strings.Contains(
			strings.ToLower(mount.Source),
			uuid,
		) || strings.Contains(
			strings.ToLower(mount.Destination),
			uuid,
		) {
			evidence = append(
				evidence,
				"mount",
			)
			break
		}
	}

	return uniqueStrings(evidence)
}

func sourceContainerActive(state string) bool {
	switch strings.ToLower(
		strings.TrimSpace(state),
	) {
	case "running", "restarting", "paused":
		return true
	default:
		return false
	}
}

func containerDisplayName(
	container dockerContainerSummary,
) string {
	if len(container.Names) > 0 {
		name := strings.TrimPrefix(
			container.Names[0],
			"/",
		)

		if name != "" {
			return name
		}
	}

	return shortContainerID(container.ID)
}

func shortContainerID(value string) string {
	value = strings.TrimSpace(value)

	if len(value) > 12 {
		return value[:12]
	}

	return value
}

func uniqueStrings(values []string) []string {
	result := []string{}
	seen := map[string]bool{}

	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}

		seen[value] = true
		result = append(result, value)
	}

	return result
}
