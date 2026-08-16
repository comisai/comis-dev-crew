package forge

import (
	"encoding/json"
	"strings"
)

func parseGitHubCheckName(raw json.RawMessage) (string, bool) {
	var name string
	if len(raw) == 0 || json.Unmarshal(raw, &name) != nil || name == "" || len(name) > 128 ||
		strings.TrimSpace(name) != name || strings.ContainsAny(name, "\x00\r\n") {
		return "", false
	}
	return name, true
}
