// Package localconfig derives installation-local product paths.
package localconfig

import (
	"errors"
	"path/filepath"
)

// Paths contains the service-owned state and caller-class endpoint locations.
type Paths struct {
	Database  string
	Socket    string
	MCPSocket string
}

// Under derives stable paths below an absolute canonical configuration root.
func Under(configurationRoot string) (Paths, error) {
	if !filepath.IsAbs(configurationRoot) || filepath.Clean(configurationRoot) != configurationRoot {
		return Paths{}, errors.New("derive local paths: configuration root must be absolute and canonical")
	}
	root := filepath.Join(configurationRoot, "comis-dev-crew")
	return Paths{
		Database:  filepath.Join(root, "state", "devcrew.db"),
		Socket:    filepath.Join(root, "run", "devcrew.sock"),
		MCPSocket: filepath.Join(root, "run", "mcp.sock"),
	}, nil
}
