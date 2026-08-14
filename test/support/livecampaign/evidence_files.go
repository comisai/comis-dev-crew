package livecampaign

import (
	"errors"
	"fmt"
	"os"
)

func ensurePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("collect live closeout: create evidence root: %w", err)
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return fmt.Errorf("collect live closeout: inspect evidence root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return errors.New("collect live closeout: evidence root must be one owner-private directory")
	}
	return nil
}

func appendArgs(prefix []string, suffix ...string) []string {
	result := make([]string, 0, len(prefix)+len(suffix))
	result = append(result, prefix...)
	return append(result, suffix...)
}
