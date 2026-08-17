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

func parseGitHubCheckID(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var identity int64
	if err := json.Unmarshal(raw, &identity); err != nil || identity < 1 {
		return 0, false
	}
	return identity, true
}
