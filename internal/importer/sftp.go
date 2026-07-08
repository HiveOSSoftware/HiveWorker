package importer

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
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
	Host       string
	Port       int
	Username   string
	Password   string
	RemotePath string
	LocalPath  string
	Options    Options
}

func TestSFTP(cfg SFTPConfig) error {
	sshClient, sftpClient, err := connectSFTP(cfg)
	if err != nil {
		return err
	}
	defer sshClient.Close()
	defer sftpClient.Close()

	remotePath := normalRemotePath(cfg.RemotePath)

	_, err = sftpClient.ReadDir(remotePath)
	return err
}

func ImportSFTP(cfg SFTPConfig, progress func(stage string, percent int, message string)) error {
	sshClient, sftpClient, err := connectSFTP(cfg)
	if err != nil {
		return err
	}
	defer sshClient.Close()
	defer sftpClient.Close()

	remotePath := normalRemotePath(cfg.RemotePath)

	progress("Preparing", 5, "Preparing remote archive...")

	remoteArchive := fmt.Sprintf("/tmp/hivepanel-import-%d.tar.gz", time.Now().UnixNano())
	localArchive := filepath.Join(os.TempDir(), filepath.Base(remoteArchive))

	defer os.Remove(localArchive)

	if cfg.Options.WipeBeforeImport {
		progress("Preparing", 10, "Wiping current server files...")
		if err := wipeDirectory(cfg.LocalPath); err != nil {
			return err
		}
	}

	if err := createRemoteArchive(sshClient, remotePath, remoteArchive, cfg.Options); err != nil {
		return err
	}

	defer func() {
		_ = runSSHCommand(sshClient, "rm -f "+shellQuote(remoteArchive))
	}()

	progress("Downloading", 45, "Downloading remote archive...")

	if err := downloadRemoteArchive(sftpClient, remoteArchive, localArchive); err != nil {
		return err
	}

	progress("Extracting", 75, "Extracting archive into server files...")

	if err := extractTarGz(localArchive, cfg.LocalPath); err != nil {
		return err
	}

	progress("Complete", 100, "Import completed successfully.")
	return nil
}

func connectSFTP(cfg SFTPConfig) (*ssh.Client, *sftp.Client, error) {
	sshConfig := &ssh.ClientConfig{
		User: cfg.Username,
		Auth: []ssh.AuthMethod{
			ssh.Password(cfg.Password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	}

	address := cfg.Host + ":" + strconv.Itoa(cfg.Port)

	sshClient, err := ssh.Dial("tcp", address, sshConfig)
	if err != nil {
		return nil, nil, err
	}

	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		_ = sshClient.Close()
		return nil, nil, err
	}

	return sshClient, sftpClient, nil
}

func createRemoteArchive(client *ssh.Client, remotePath string, remoteArchive string, options Options) error {
	includes := selectedIncludes(options)
	if len(includes) == 0 {
		return errors.New("nothing selected to import")
	}

	args := make([]string, 0, len(includes))
	for _, include := range includes {
		args = append(args, shellQuote(include))
	}

	command := fmt.Sprintf(
		"cd %s && tar -czf %s --ignore-failed-read %s",
		shellQuote(remotePath),
		shellQuote(remoteArchive),
		strings.Join(args, " "),
	)

	return runSSHCommand(client, command)
}

func selectedIncludes(options Options) []string {
	includes := make([]string, 0)

	if options.ImportWorlds {
		includes = append(includes, "world", "world_nether", "world_the_end")
	}

	if options.ImportPlugins {
		includes = append(includes, "plugins")
	}

	if options.ImportMods {
		includes = append(includes, "mods")
	}

	if options.ImportConfigs {
		includes = append(includes,
			"server.properties",
			"bukkit.yml",
			"spigot.yml",
			"paper.yml",
			"purpur.yml",
			"config",
		)
	}

	if options.ImportServerJar {
		includes = append(includes, "*.jar")
	}

	return includes
}

func runSSHCommand(client *ssh.Client, command string) error {
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	output, err := session.CombinedOutput(command)
	if err != nil {
		return fmt.Errorf("%s: %s", err.Error(), strings.TrimSpace(string(output)))
	}

	return nil
}

func downloadRemoteArchive(client *sftp.Client, remoteArchive string, localArchive string) error {
	source, err := client.Open(remoteArchive)
	if err != nil {
		return err
	}
	defer source.Close()

	target, err := os.OpenFile(localArchive, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer target.Close()

	_, err = io.Copy(target, source)
	return err
}

func extractTarGz(archivePath string, targetDir string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)

	baseAbs, err := filepath.Abs(targetDir)
	if err != nil {
		return err
	}

	for {
		header, err := tarReader.Next()

		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return err
		}

		cleanName := filepath.Clean(header.Name)

		if cleanName == "." || strings.HasPrefix(cleanName, "..") || filepath.IsAbs(cleanName) {
			continue
		}

		targetPath := filepath.Join(targetDir, cleanName)
		targetAbs, err := filepath.Abs(targetPath)
		if err != nil {
			return err
		}

		if !strings.HasPrefix(targetAbs, baseAbs) {
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, os.FileMode(header.Mode)); err != nil {
				return err
			}

		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return err
			}

			target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode))
			if err != nil {
				return err
			}

			if _, err := io.Copy(target, tarReader); err != nil {
				_ = target.Close()
				return err
			}

			if err := target.Close(); err != nil {
				return err
			}
		}
	}

	return nil
}

func wipeDirectory(path string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		name := entry.Name()

		if name == ".hivepanel" || name == ".recycle_bin" {
			continue
		}

		if err := os.RemoveAll(filepath.Join(path, name)); err != nil {
			return err
		}
	}

	return nil
}

func normalRemotePath(path string) string {
	if strings.TrimSpace(path) == "" {
		return "."
	}

	return path
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
