package forge

import "encoding/json"

func parseGitHubCheckState(statusRaw, conclusionRaw json.RawMessage) (string, *string, bool) {
	if len(statusRaw) == 0 || len(conclusionRaw) == 0 {
		return "", nil, false
	}
	var status *string
	if err := json.Unmarshal(statusRaw, &status); err != nil || status == nil {
		return "", nil, false
	}
	var conclusion *string
	if err := json.Unmarshal(conclusionRaw, &conclusion); err != nil {
		return "", nil, false
	}
	return *status, conclusion, true
}
