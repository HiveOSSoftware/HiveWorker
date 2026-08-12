package importer

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type Options struct {
	ImportWorlds     bool `json:"importWorlds"`
	ImportPlugins    bool `json:"importPlugins"`
	ImportConfigs    bool `json:"importConfigs"`
	ImportMods       bool `json:"importMods"`
	ImportServerJar  bool `json:"importServerJar"`
	WipeBeforeImport bool `json:"wipeBeforeImport"`
}

type SFTPConfig struct {
	Host                 string
	Port                 int
	Username             string
	AuthType             string
	Password             string
	PrivateKey           string
	PrivateKeyPassphrase string
	RemotePath           string
	LocalPath            string
	Options              Options
}

func TestSFTP(cfg SFTPConfig) error {
	sshClient, client, err := connectSFTP(cfg)
	if err != nil {
		return err
	}
	defer sshClient.Close()
	defer client.Close()

	remotePath := normaliseRemotePath(cfg.RemotePath)

	info, err := client.Stat(remotePath)
	if err != nil {
		return fmt.Errorf("unable to access remote path %s: %w", remotePath, err)
	}

	if !info.IsDir() {
		return fmt.Errorf("remote path %s is not a directory", remotePath)
	}

	return nil
}

func ImportSFTP(
	cfg SFTPConfig,
	progress func(stage string, percent int, message string),
) error {
	if strings.TrimSpace(cfg.LocalPath) == "" {
		return errors.New("local import path is required")
	}

	sshClient, client, err := connectSFTP(cfg)
	if err != nil {
		return err
	}
	defer sshClient.Close()
	defer client.Close()

	remoteRoot := normaliseRemotePath(cfg.RemotePath)

	if progress != nil {
		progress("Scanning", 15, "Scanning source server files...")
	}

	files, err := collectRemoteFiles(client, remoteRoot, cfg.Options)
	if err != nil {
		return err
	}

	if cfg.Options.WipeBeforeImport {
		if progress != nil {
			progress("Preparing", 20, "Removing existing destination files...")
		}

		if err := wipeLocalDirectory(cfg.LocalPath); err != nil {
			return err
		}
	}

	if err := os.MkdirAll(cfg.LocalPath, 0755); err != nil {
		return err
	}

	totalBytes := int64(0)
	for _, file := range files {
		totalBytes += file.Size
	}

	var copiedBytes int64

	for index, file := range files {
		relative := strings.TrimPrefix(file.Path, remoteRoot)
		relative = strings.TrimPrefix(relative, "/")

		localPath := filepath.Join(
			cfg.LocalPath,
			filepath.FromSlash(relative),
		)

		if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
			return err
		}

		if progress != nil {
			percent := transferPercent(
				index,
				len(files),
				copiedBytes,
				totalBytes,
			)

			progress(
				"Transferring",
				percent,
				"Copying "+relative,
			)
		}

		copied, err := copyRemoteFile(
			client,
			file.Path,
			localPath,
			file.Mode,
		)
		if err != nil {
			return fmt.Errorf("copy %s: %w", relative, err)
		}

		copiedBytes += copied
	}

	if progress != nil {
		progress("Finalizing", 92, "Finalizing imported files...")
	}

	return nil
}

type remoteFile struct {
	Path string
	Size int64
	Mode os.FileMode
}

func connectSFTP(cfg SFTPConfig) (*ssh.Client, *sftp.Client, error) {
	host := strings.TrimSpace(cfg.Host)
	username := strings.TrimSpace(cfg.Username)

	if host == "" {
		return nil, nil, errors.New("SFTP host is required")
	}

	if username == "" {
		return nil, nil, errors.New("SFTP username is required")
	}

	port := cfg.Port
	if port <= 0 {
		port = 22
	}

	authMethods, err := sshAuthMethods(cfg)
	if err != nil {
		return nil, nil, err
	}

	sshConfig := &ssh.ClientConfig{
		User:            username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	address := net.JoinHostPort(
		host,
		fmt.Sprintf("%d", port),
	)

	sshClient, err := ssh.Dial(
		"tcp",
		address,
		sshConfig,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("SFTP SSH connection failed: %w", err)
	}

	client, err := sftp.NewClient(sshClient)
	if err != nil {
		_ = sshClient.Close()
		return nil, nil, fmt.Errorf("SFTP client initialization failed: %w", err)
	}

	return sshClient, client, nil
}

func sshAuthMethods(cfg SFTPConfig) ([]ssh.AuthMethod, error) {
	authType := strings.TrimSpace(
		strings.ToLower(cfg.AuthType),
	)

	if authType == "" {
		if strings.TrimSpace(cfg.PrivateKey) != "" {
			authType = "private_key"
		} else {
			authType = "password"
		}
	}

	switch authType {
	case "password":
		if cfg.Password == "" {
			return nil, errors.New("SFTP password is required")
		}

		return []ssh.AuthMethod{
			ssh.Password(cfg.Password),
		}, nil

	case "private_key":
		privateKey := []byte(cfg.PrivateKey)

		if len(strings.TrimSpace(cfg.PrivateKey)) == 0 {
			return nil, errors.New("SFTP private key is required")
		}

		var signer ssh.Signer
		var err error

		if cfg.PrivateKeyPassphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(
				privateKey,
				[]byte(cfg.PrivateKeyPassphrase),
			)
		} else {
			signer, err = ssh.ParsePrivateKey(privateKey)
		}
		if err != nil {
			return nil, fmt.Errorf("invalid SFTP private key: %w", err)
		}

		return []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		}, nil

	default:
		return nil, fmt.Errorf("unsupported SFTP auth type: %s", authType)
	}
}

func collectRemoteFiles(
	client *sftp.Client,
	remoteRoot string,
	options Options,
) ([]remoteFile, error) {
	result := []remoteFile{}

	walker := client.Walk(remoteRoot)

	for walker.Step() {
		if err := walker.Err(); err != nil {
			return nil, err
		}

		info := walker.Stat()
		if info == nil || info.IsDir() {
			continue
		}

		remotePath := walker.Path()

		relative := strings.TrimPrefix(
			remotePath,
			remoteRoot,
		)
		relative = strings.TrimPrefix(relative, "/")

		if !shouldImport(relative, options) {
			continue
		}

		result = append(result, remoteFile{
			Path: remotePath,
			Size: info.Size(),
			Mode: info.Mode(),
		})
	}

	return result, nil
}

func shouldImport(relative string, options Options) bool {
	clean := strings.TrimPrefix(
		path.Clean("/"+relative),
		"/",
	)

	if clean == "." || clean == "" {
		return false
	}

	first := strings.ToLower(
		strings.Split(clean, "/")[0],
	)

	if first == "plugins" && !options.ImportPlugins {
		return false
	}

	if first == "mods" && !options.ImportMods {
		return false
	}

	if isWorldPath(first) && !options.ImportWorlds {
		return false
	}

	if isConfigPath(clean) && !options.ImportConfigs {
		return false
	}

	if isServerJar(clean) && !options.ImportServerJar {
		return false
	}

	return true
}

func isWorldPath(first string) bool {
	return first == "world" ||
		strings.HasPrefix(first, "world_") ||
		first == "worlds"
}

func isConfigPath(relative string) bool {
	lower := strings.ToLower(relative)

	if strings.HasPrefix(lower, "config/") {
		return true
	}

	base := strings.ToLower(path.Base(lower))

	return strings.HasSuffix(base, ".yml") ||
		strings.HasSuffix(base, ".yaml") ||
		strings.HasSuffix(base, ".json") ||
		strings.HasSuffix(base, ".properties") ||
		strings.HasSuffix(base, ".toml") ||
		strings.HasSuffix(base, ".conf") ||
		strings.HasSuffix(base, ".cfg") ||
		strings.HasSuffix(base, ".ini")
}

func isServerJar(relative string) bool {
	lower := strings.ToLower(
		path.Base(relative),
	)

	return lower == "server.jar" ||
		strings.HasSuffix(lower, ".jar")
}

func copyRemoteFile(
	client *sftp.Client,
	remotePath string,
	localPath string,
	mode os.FileMode,
) (int64, error) {
	source, err := client.Open(remotePath)
	if err != nil {
		return 0, err
	}
	defer source.Close()

	destination, err := os.OpenFile(
		localPath,
		os.O_CREATE|os.O_TRUNC|os.O_WRONLY,
		mode.Perm(),
	)
	if err != nil {
		return 0, err
	}
	defer destination.Close()

	return io.Copy(destination, source)
}

func wipeLocalDirectory(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return err
	}

	for _, entry := range entries {
		if entry.Name() == ".hivepanel" {
			continue
		}

		if err := os.RemoveAll(
			filepath.Join(
				directory,
				entry.Name(),
			),
		); err != nil {
			return err
		}
	}

	return nil
}

func normaliseRemotePath(remotePath string) string {
	remotePath = strings.TrimSpace(remotePath)

	if remotePath == "" {
		return "."
	}

	return path.Clean(remotePath)
}

func transferPercent(
	index int,
	totalFiles int,
	copiedBytes int64,
	totalBytes int64,
) int {
	if totalBytes > 0 {
		ratio := float64(copiedBytes) /
			float64(totalBytes)

		return 25 + int(ratio*65)
	}

	if totalFiles > 0 {
		ratio := float64(index) /
			float64(totalFiles)

		return 25 + int(ratio*65)
	}

	return 90
}
