package migrationdetect

import "errors"

func freeSpace(path string) (int64, error) {
	return 0, errors.New("filesystem free-space detection is not supported on Windows")
}
