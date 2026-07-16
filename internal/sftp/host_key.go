package sftp

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

const hostKeyFileMode = 0600

// LoadOrCreateHostKey loads the persistent SSH host key used by the
// HivePanel SFTP server. If it does not exist, an Ed25519 key is generated.
func LoadOrCreateHostKey(path string) (ssh.Signer, error) {
	if path == "" {
		return nil, fmt.Errorf("SFTP host key path is empty")
	}

	data, err := os.ReadFile(path)
	if err == nil {
		return parseHostKey(path, data)
	}

	if !os.IsNotExist(err) {
		return nil, fmt.Errorf(
			"read SFTP host key %q: %w",
			path,
			err,
		)
	}

	return createHostKey(path)
}

func createHostKey(path string) (ssh.Signer, error) {
	parent := filepath.Dir(path)

	if err := os.MkdirAll(parent, 0700); err != nil {
		return nil, fmt.Errorf(
			"create SFTP host key directory %q: %w",
			parent,
			err,
		)
	}

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf(
			"generate Ed25519 SFTP host key: %w",
			err,
		)
	}

	privateKeyBlock, err := ssh.MarshalPrivateKey(
		privateKey,
		"HivePanel Worker SFTP Host Key",
	)
	if err != nil {
		return nil, fmt.Errorf(
			"marshal SFTP host key: %w",
			err,
		)
	}

	encoded := pem.EncodeToMemory(privateKeyBlock)
	if len(encoded) == 0 {
		return nil, fmt.Errorf("encode SFTP host key: empty PEM result")
	}

	file, err := os.OpenFile(
		path,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		hostKeyFileMode,
	)
	if err != nil {
		if os.IsExist(err) {
			/*
				Another worker process created the key while this process
				was generating one. Always load the actual stored key.
			*/
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil, fmt.Errorf(
					"read concurrently created SFTP host key %q: %w",
					path,
					readErr,
				)
			}

			return parseHostKey(path, data)
		}

		return nil, fmt.Errorf(
			"create SFTP host key %q: %w",
			path,
			err,
		)
	}

	writeSucceeded := false

	defer func() {
		_ = file.Close()

		if !writeSucceeded {
			_ = os.Remove(path)
		}
	}()

	if _, err := file.Write(encoded); err != nil {
		return nil, fmt.Errorf(
			"write SFTP host key %q: %w",
			path,
			err,
		)
	}

	if err := file.Sync(); err != nil {
		return nil, fmt.Errorf(
			"sync SFTP host key %q: %w",
			path,
			err,
		)
	}

	if err := file.Chmod(hostKeyFileMode); err != nil {
		return nil, fmt.Errorf(
			"set SFTP host key permissions %q: %w",
			path,
			err,
		)
	}

	if err := file.Close(); err != nil {
		return nil, fmt.Errorf(
			"close SFTP host key %q: %w",
			path,
			err,
		)
	}

	writeSucceeded = true

	return parseHostKey(path, encoded)
}

func parseHostKey(path string, data []byte) (ssh.Signer, error) {
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf(
			"parse SFTP host key %q: %w",
			path,
			err,
		)
	}

	return signer, nil
}
