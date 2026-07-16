package sftp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	pkgsftp "github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"hivepanel-worker/internal/config"
	"hivepanel-worker/internal/panel"
)

const (
	sshPermissionsAuthKey = "hivepanel-sftp-auth"

	maxSSHConnections   = 128
	sshHandshakeTimeout = 15 * time.Second
)

// HandlerFactory creates the custom SFTP request handlers for one
// authenticated session.
//
// The implementation must jail every filesystem operation inside root.
type HandlerFactory interface {
	Create(
		root string,
		permissions panel.SFTPPermissions,
	) (pkgsftp.Handlers, error)
}

type Server struct {
	config         config.Config
	authenticator  panel.SFTPAuthenticator
	handlerFactory HandlerFactory

	sshConfig *ssh.ServerConfig
	listener  net.Listener

	connectionSlots chan struct{}

	closeOnce sync.Once
	closed    chan struct{}
}

type authenticatedSession struct {
	Username     string                `json:"username"`
	CredentialID string                `json:"credential_id"`
	UserID       string                `json:"user_id"`
	CellID       string                `json:"cell_id"`
	DaemonID     string                `json:"daemon_id"`
	Root         string                `json:"root"`
	Permissions  panel.SFTPPermissions `json:"permissions"`
}

type subsystemRequest struct {
	Subsystem string
}

func NewServer(
	cfg config.Config,
	authenticator panel.SFTPAuthenticator,
	handlerFactory HandlerFactory,
) (*Server, error) {
	if !cfg.SFTP.Enabled {
		return nil, fmt.Errorf("SFTP server is disabled")
	}

	if authenticator == nil {
		return nil, fmt.Errorf("SFTP authenticator is required")
	}

	if handlerFactory == nil {
		return nil, fmt.Errorf("SFTP handler factory is required")
	}

	hostKey, err := LoadOrCreateHostKey(cfg.SFTP.HostKeyPath)
	if err != nil {
		return nil, err
	}

	server := &Server{
		config:          cfg,
		authenticator:   authenticator,
		handlerFactory:  handlerFactory,
		connectionSlots: make(chan struct{}, maxSSHConnections),
		closed:          make(chan struct{}),
	}

	server.sshConfig = &ssh.ServerConfig{
		ServerVersion: "SSH-2.0-HivePanel-SFTP",

		PasswordCallback: server.authenticatePassword,

		MaxAuthTries: 5,

		NoClientAuth: false,
	}

	server.sshConfig.AddHostKey(hostKey)

	return server, nil
}

func (server *Server) ListenAndServe() error {
	if strings.TrimSpace(server.config.SFTP.Listen) == "" {
		return fmt.Errorf("SFTP listen address is empty")
	}

	listener, err := net.Listen("tcp", server.config.SFTP.Listen)
	if err != nil {
		return fmt.Errorf(
			"listen for SFTP connections on %q: %w",
			server.config.SFTP.Listen,
			err,
		)
	}

	server.listener = listener

	for {
		connection, err := listener.Accept()
		if err != nil {
			select {
			case <-server.closed:
				return nil
			default:
				return fmt.Errorf("accept SFTP connection: %w", err)
			}
		}

		select {
		case server.connectionSlots <- struct{}{}:
			go func() {
				defer func() {
					<-server.connectionSlots
				}()

				server.handleConnection(connection)
			}()

		default:
			log.Printf(
				"SFTP connection rejected from %s: connection limit reached",
				connection.RemoteAddr(),
			)

			_ = connection.Close()
		}
	}
}

func (server *Server) Close() error {
	var closeErr error

	server.closeOnce.Do(func() {
		close(server.closed)

		if server.listener != nil {
			closeErr = server.listener.Close()
		}
	})

	return closeErr
}

func (server *Server) authenticatePassword(
	metadata ssh.ConnMetadata,
	password []byte,
) (*ssh.Permissions, error) {
	username := strings.TrimSpace(metadata.User())

	if username == "" || len(password) == 0 {
		return nil, errors.New("authentication failed")
	}

	timeout := time.Duration(
		server.config.SFTP.AuthTimeoutSeconds,
	) * time.Second

	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	ctx, cancel := context.WithTimeout(
		context.Background(),
		timeout,
	)
	defer cancel()

	result, err := server.authenticator.Authenticate(
		ctx,
		username,
		string(password),
	)
	if err != nil {
		if errors.Is(err, panel.ErrSFTPAuthenticationDenied) {
			log.Printf(
				"SFTP authentication denied for %q from %s",
				username,
				metadata.RemoteAddr(),
			)
		} else {
			log.Printf(
				"SFTP authentication error for %q from %s: %v",
				username,
				metadata.RemoteAddr(),
				err,
			)
		}

		return nil, errors.New("authentication failed")
	}

	root, err := server.resolveCellRoot(result.DaemonID)
	if err != nil {
		log.Printf(
			"SFTP root resolution failed for %q: %v",
			username,
			err,
		)

		return nil, errors.New("authentication failed")
	}

	session := authenticatedSession{
		Username:     username,
		CredentialID: result.CredentialID,
		UserID:       result.UserID,
		CellID:       result.CellID,
		DaemonID:     result.DaemonID,
		Root:         root,
		Permissions:  result.Permissions,
	}

	encoded, err := json.Marshal(session)
	if err != nil {
		log.Printf(
			"SFTP session encoding failed for %q: %v",
			username,
			err,
		)

		return nil, errors.New("authentication failed")
	}

	return &ssh.Permissions{
		Extensions: map[string]string{
			sshPermissionsAuthKey: string(encoded),
		},
	}, nil
}

func (server *Server) resolveCellRoot(
	daemonID string,
) (string, error) {
	daemonID = strings.TrimSpace(daemonID)

	if daemonID == "" {
		return "", fmt.Errorf("daemon ID is empty")
	}

	/*
		Daemon IDs are used as one directory component. Reject anything
		that could be interpreted as a path.
	*/
	if daemonID == "." ||
		daemonID == ".." ||
		filepath.IsAbs(daemonID) ||
		strings.Contains(daemonID, "/") ||
		strings.Contains(daemonID, `\`) {

		return "", fmt.Errorf("invalid daemon ID")
	}

	instancesRoot, err := filepath.Abs(
		server.config.Paths.Instances,
	)
	if err != nil {
		return "", fmt.Errorf(
			"resolve instances directory: %w",
			err,
		)
	}

	root, err := filepath.Abs(
		filepath.Join(instancesRoot, daemonID),
	)
	if err != nil {
		return "", fmt.Errorf(
			"resolve cell directory: %w",
			err,
		)
	}

	relative, err := filepath.Rel(instancesRoot, root)
	if err != nil {
		return "", fmt.Errorf(
			"validate cell directory: %w",
			err,
		)
	}

	if relative == "." ||
		relative == ".." ||
		strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {

		return "", fmt.Errorf(
			"cell directory escapes instances directory",
		)
	}

	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf(
				"cell directory does not exist",
			)
		}

		return "", fmt.Errorf(
			"inspect cell directory: %w",
			err,
		)
	}

	if !info.IsDir() {
		return "", fmt.Errorf(
			"cell root is not a directory",
		)
	}

	return root, nil
}

func (server *Server) handleConnection(
	connection net.Conn,
) {
	defer connection.Close()

	remoteAddress := connection.RemoteAddr().String()

	/*
		Prevent clients from opening a TCP connection and never completing
		the SSH handshake.
	*/
	_ = connection.SetDeadline(
		time.Now().Add(sshHandshakeTimeout),
	)

	sshConnection, channels, requests, err := ssh.NewServerConn(
		connection,
		server.sshConfig,
	)
	if err != nil {
		log.Printf(
			"SFTP SSH handshake failed from %s: %v",
			remoteAddress,
			err,
		)

		return
	}

	/*
		Remove the handshake deadline once authentication succeeds. File
		transfers may legitimately run for a long time.
	*/
	_ = connection.SetDeadline(time.Time{})

	defer sshConnection.Close()

	session, err := authenticatedSessionFromPermissions(
		sshConnection.Permissions,
	)
	if err != nil {
		log.Printf(
			"SFTP authenticated session missing metadata from %s: %v",
			remoteAddress,
			err,
		)

		return
	}

	log.Printf(
		"SFTP user %q connected from %s for cell %s",
		session.Username,
		remoteAddress,
		session.CellID,
	)

	defer log.Printf(
		"SFTP user %q disconnected from %s for cell %s",
		session.Username,
		remoteAddress,
		session.CellID,
	)

	/*
		Reject SSH global requests, including TCP forwarding requests.
	*/
	go rejectGlobalRequests(requests)

	var waitGroup sync.WaitGroup

	for newChannel := range channels {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(
				ssh.UnknownChannelType,
				"only SFTP sessions are supported",
			)

			continue
		}

		channel, channelRequests, err := newChannel.Accept()
		if err != nil {
			log.Printf(
				"Failed to accept SFTP SSH channel for %q: %v",
				session.Username,
				err,
			)

			continue
		}

		waitGroup.Add(1)

		go func() {
			defer waitGroup.Done()
			defer channel.Close()

			server.handleSessionChannel(
				channel,
				channelRequests,
				session,
			)
		}()
	}

	waitGroup.Wait()
}

func (server *Server) handleSessionChannel(
	channel ssh.Channel,
	requests <-chan *ssh.Request,
	session authenticatedSession,
) {
	started := false

	for request := range requests {
		switch request.Type {
		case "subsystem":
			if started {
				_ = request.Reply(false, nil)
				continue
			}

			var payload subsystemRequest

			if err := ssh.Unmarshal(
				request.Payload,
				&payload,
			); err != nil {
				_ = request.Reply(false, nil)
				continue
			}

			if payload.Subsystem != "sftp" {
				_ = request.Reply(false, nil)
				continue
			}

			if err := request.Reply(true, nil); err != nil {
				return
			}

			started = true

			if err := server.serveSFTP(
				channel,
				session,
			); err != nil &&
				!errors.Is(err, io.EOF) {

				log.Printf(
					"SFTP session error for %q on cell %s: %v",
					session.Username,
					session.CellID,
					err,
				)
			}

			return

		/*
			Explicitly reject shell access, command execution, PTY
			allocation, environment modification and agent forwarding.
		*/
		case "shell",
			"exec",
			"pty-req",
			"env",
			"auth-agent-req@openssh.com",
			"window-change",
			"signal":

			_ = request.Reply(false, nil)

		default:
			_ = request.Reply(false, nil)
		}
	}
}

func (server *Server) serveSFTP(
	channel ssh.Channel,
	session authenticatedSession,
) error {
	handlers, err := server.handlerFactory.Create(
		session.Root,
		session.Permissions,
	)
	if err != nil {
		return fmt.Errorf(
			"create jailed SFTP handlers: %w",
			err,
		)
	}

	requestServer := pkgsftp.NewRequestServer(
		channel,
		handlers,
		pkgsftp.WithStartDirectory("/"),
	)

	defer requestServer.Close()

	err = requestServer.Serve()
	if err == nil || errors.Is(err, io.EOF) {
		return nil
	}

	return err
}

func authenticatedSessionFromPermissions(
	permissions *ssh.Permissions,
) (authenticatedSession, error) {
	if permissions == nil {
		return authenticatedSession{},
			fmt.Errorf("SSH permissions are missing")
	}

	encoded := permissions.Extensions[sshPermissionsAuthKey]
	if encoded == "" {
		return authenticatedSession{},
			fmt.Errorf("authenticated session extension is missing")
	}

	var session authenticatedSession

	if err := json.Unmarshal(
		[]byte(encoded),
		&session,
	); err != nil {
		return authenticatedSession{},
			fmt.Errorf(
				"decode authenticated session: %w",
				err,
			)
	}

	if session.Username == "" ||
		session.CellID == "" ||
		session.DaemonID == "" ||
		session.Root == "" {

		return authenticatedSession{},
			fmt.Errorf("authenticated session is incomplete")
	}

	return session, nil
}

func rejectGlobalRequests(
	requests <-chan *ssh.Request,
) {
	for request := range requests {
		/*
			Reject forwarding and all other connection-wide SSH features.
		*/
		_ = request.Reply(false, nil)
	}
}
