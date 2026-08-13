package livecampaign

import (
	"errors"

	"github.com/comisai/comis-dev-crew/internal/application"
)

// MessageReport is the bounded subset consumed from `comis messages --format json`.
type MessageReport struct {
	Schema        string              `json:"schema"`
	SchemaVersion int                 `json:"schemaVersion"`
	Messages      []ChannelMessage    `json:"messages"`
	Completeness  MessageCompleteness `json:"completeness"`
}

type ChannelMessage struct {
	MessageID   *string `json:"messageId"`
	EpochMs     int64   `json:"epochMs"`
	ChannelType string  `json:"channelType"`
	SenderID    string  `json:"senderId"`
	Text        string  `json:"text"`
	AgentID     string  `json:"agentId"`
	ChatID      string  `json:"chatId"`
	SessionKey  string  `json:"sessionKey"`
	Origin      string  `json:"origin"`
}

type MessageCompleteness struct {
	Complete bool     `json:"complete"`
	Reasons  []string `json:"reasons"`
}

type CheckpointEvidence struct {
	Kind       string `json:"kind"`
	ChatID     string `json:"chatId"`
	SenderID   string `json:"senderId"`
	EpochMs    int64  `json:"epochMs"`
	MessageID  string `json:"messageId"`
	SessionKey string `json:"sessionKey"`
}

type GitHubPull struct {
	Number  int    `json:"number"`
	State   string `json:"state"`
	Merged  bool   `json:"merged"`
	HTMLURL string `json:"html_url"`
	Head    struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

type GitHubChecks struct {
	Runs []GitHubCheckRun `json:"check_runs"`
}

type GitHubCheckRun struct {
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	Conclusion *string `json:"conclusion"`
}

func VerifyTask(Manifest, TaskExpectation, application.TaskDetail) error {
	return errors.New("live task evidence verification not implemented")
}

func VerifyOperation(OperationExpectation, application.OperationView) error {
	return errors.New("live operation evidence verification not implemented")
}

func VerifyMessages(Manifest, MessageReport) ([]CheckpointEvidence, error) {
	return nil, errors.New("live Telegram evidence verification not implemented")
}

func VerifyPullRequest(GitHubTarget, application.TaskDetail, GitHubPull, GitHubChecks) error {
	return errors.New("live GitHub evidence verification not implemented")
}
