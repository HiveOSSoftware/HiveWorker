package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"hivepanel-worker/internal/config"
)

var (
	ErrSFTPAuthenticationDenied = errors.New("SFTP authentication denied")
	ErrSFTPPanelUnavailable     = errors.New("panel unavailable during SFTP authentication")
)

type SFTPAuthClient struct {
	panelURL string
	nodeID   string
	token    string
	client   *http.Client
}

type SFTPAuthRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type SFTPAuthResponse struct {
	Allowed      bool            `json:"allowed"`
	Message      string          `json:"message,omitempty"`
	CredentialID string          `json:"credential_id,omitempty"`
	UserID       string          `json:"user_id,omitempty"`
	CellID       string          `json:"cell_id,omitempty"`
	DaemonID     string          `json:"daemon_id,omitempty"`
	Permissions  SFTPPermissions `json:"permissions,omitempty"`
}

type SFTPPermissions struct {
	Read   bool `json:"read"`
	Write  bool `json:"write"`
	Create bool `json:"create"`
	Rename bool `json:"rename"`
	Delete bool `json:"delete"`
}

func NewSFTPAuthClient(cfg config.Config) *SFTPAuthClient {
	timeout := time.Duration(cfg.SFTP.AuthTimeoutSeconds) * time.Second

	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	return &SFTPAuthClient{
		panelURL: strings.TrimRight(cfg.Panel.URL, "/"),
		nodeID:   cfg.Node.ID,
		token:    cfg.Worker.Token,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

func (client *SFTPAuthClient) Authenticate(
	ctx context.Context,
	username string,
	password string,
) (*SFTPAuthResponse, error) {
	if strings.TrimSpace(username) == "" {
		return nil, ErrSFTPAuthenticationDenied
	}

	if password == "" {
		return nil, ErrSFTPAuthenticationDenied
	}

	if client.panelURL == "" {
		return nil, fmt.Errorf(
			"%w: panel URL is empty",
			ErrSFTPPanelUnavailable,
		)
	}

	if client.nodeID == "" {
		return nil, fmt.Errorf(
			"%w: node ID is empty",
			ErrSFTPPanelUnavailable,
		)
	}

	if client.token == "" {
		return nil, fmt.Errorf(
			"%w: worker token is empty",
			ErrSFTPPanelUnavailable,
		)
	}

	payload, err := json.Marshal(SFTPAuthRequest{
		Username: username,
		Password: password,
	})
	if err != nil {
		return nil, fmt.Errorf(
			"encode SFTP authentication request: %w",
			err,
		)
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		client.panelURL+"/api/worker/sftp/auth",
		bytes.NewReader(payload),
	)
	if err != nil {
		return nil, fmt.Errorf(
			"create SFTP authentication request: %w",
			err,
		)
	}

	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("X-Hive-Node", client.nodeID)
	request.Header.Set("User-Agent", "HivePanel-Worker/SFTP")

	response, err := client.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: %v",
			ErrSFTPPanelUnavailable,
			err,
		)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024))
	if err != nil {
		return nil, fmt.Errorf(
			"read SFTP authentication response: %w",
			err,
		)
	}

	var result SFTPAuthResponse

	if len(body) > 0 {
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf(
				"decode SFTP authentication response: %w",
				err,
			)
		}
	}

	switch response.StatusCode {
	case http.StatusOK:
		if !result.Allowed {
			return nil, ErrSFTPAuthenticationDenied
		}

		if result.DaemonID == "" {
			return nil, fmt.Errorf(
				"panel allowed SFTP login without daemon_id",
			)
		}

		return &result, nil

	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, ErrSFTPAuthenticationDenied

	default:
		message := strings.TrimSpace(result.Message)

		if message == "" {
			message = strings.TrimSpace(string(body))
		}

		if message == "" {
			message = http.StatusText(response.StatusCode)
		}

		return nil, fmt.Errorf(
			"%w: HTTP %d: %s",
			ErrSFTPPanelUnavailable,
			response.StatusCode,
			message,
		)
	}
}
