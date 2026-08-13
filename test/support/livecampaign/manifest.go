package livecampaign

import "errors"

// Manifest is the strict, content-free authority for one protected E0 campaign.
type Manifest struct {
	SchemaVersion int                    `json:"schemaVersion"`
	CampaignID    string                 `json:"campaignId"`
	StartedAtMs   int64                  `json:"startedAtMs"`
	EndedAtMs     int64                  `json:"endedAtMs"`
	DevCrew       DevCrewTarget          `json:"devcrew"`
	Comis         ComisTarget            `json:"comis"`
	Telegram      TelegramTarget         `json:"telegram"`
	GitHub        GitHubTarget           `json:"github"`
	Services      ServiceTargets         `json:"services"`
	Tasks         []TaskExpectation      `json:"tasks"`
	Operations    []OperationExpectation `json:"operations"`
}

type DevCrewTarget struct {
	CLIPath      string `json:"cliPath"`
	SocketPath   string `json:"socketPath"`
	RepositoryID string `json:"repositoryId"`
}

type ComisTarget struct {
	NodePath              string   `json:"nodePath"`
	CLIScriptPath         string   `json:"cliScriptPath"`
	CodeRoot              string   `json:"codeRoot"`
	DataDir               string   `json:"dataDir"`
	AgentID               string   `json:"agentId"`
	SecretResidencyScript string   `json:"secretResidencyScript"`
	SecretNames           []string `json:"secretNames"`
	ExplainRefs           []string `json:"explainRefs"`
}

type TelegramTarget struct {
	OriginChatID string               `json:"originChatId"`
	NewerChatID  string               `json:"newerChatId"`
	SenderID     string               `json:"senderId"`
	Checkpoints  []TelegramCheckpoint `json:"checkpoints"`
}

type TelegramCheckpoint struct {
	Kind   string `json:"kind"`
	ChatID string `json:"chatId"`
	Marker string `json:"marker"`
}

type GitHubTarget struct {
	CLIPath         string   `json:"cliPath"`
	Repository      string   `json:"repository"`
	PrimaryCheckout string   `json:"primaryCheckout"`
	BaseBranch      string   `json:"baseBranch"`
	RequiredChecks  []string `json:"requiredChecks"`
}

type ServiceTargets struct {
	SystemctlPath  string `json:"systemctlPath"`
	IsolationLabel string `json:"isolationLabel"`
	MCPUnit        string `json:"mcpUnit"`
	DevCrewUnit    string `json:"devcrewUnit"`
	ComisUnit      string `json:"comisUnit"`
}

type TaskExpectation struct {
	TaskHandle           string `json:"taskHandle"`
	WorkerProfileID      string `json:"workerProfileId"`
	ManagedRunID         string `json:"managedRunId"`
	ExpectReconciliation bool   `json:"expectReconciliation"`
}

type OperationExpectation struct {
	OperationID string `json:"operationId"`
	TaskHandle  string `json:"taskHandle"`
	Command     string `json:"command"`
}

// LoadManifest decodes and validates one protected campaign manifest.
func LoadManifest(path string) (Manifest, error) {
	return Manifest{}, errors.New("live campaign manifest validation not implemented")
}
