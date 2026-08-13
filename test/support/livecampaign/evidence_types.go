package livecampaign

type EvidenceCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type Verdict struct {
	SchemaVersion     int             `json:"schemaVersion"`
	CampaignID        string          `json:"campaignId"`
	CapturedAtMs      int64           `json:"capturedAtMs"`
	Passed            bool            `json:"passed"`
	EvidenceDirectory string          `json:"evidenceDirectory"`
	Checks            []EvidenceCheck `json:"checks"`
}

type gitTruth struct {
	Repository      string `json:"repository"`
	PrimaryCheckout string `json:"primaryCheckout"`
	BaseBranch      string `json:"baseBranch"`
	BaseRevision    string `json:"baseRevision"`
	PrimaryClean    bool   `json:"primaryClean"`
}

type pullRequestEvidence struct {
	TaskHandle  string           `json:"taskHandle"`
	PullRequest GitHubPull       `json:"pullRequest"`
	Checks      []GitHubCheckRun `json:"checks"`
}

type residencyReport struct {
	SchemaVersion int      `json:"schemaVersion"`
	ScannedFiles  int      `json:"scannedFiles"`
	ReadErrors    []string `json:"readErrors"`
	TotalMatches  int      `json:"totalMatches"`
	Secrets       map[string]struct {
		Retrieved    bool `json:"retrieved"`
		TotalMatches int  `json:"totalMatches"`
	} `json:"secrets"`
}

type artifactHash struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}
