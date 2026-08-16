package forge

import (
	"encoding/json"
	"time"
)

func parseGitHubCheckRecency(raw json.RawMessage) (time.Time, bool) {
	if len(raw) == 0 {
		return time.Time{}, false
	}
	var value *string
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return time.Time{}, false
	}
	startedAt, err := time.Parse(time.RFC3339, *value)
	if err != nil || startedAt.IsZero() {
		return time.Time{}, false
	}
	return startedAt, true
}
