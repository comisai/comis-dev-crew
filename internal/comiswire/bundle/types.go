// Package bundle verifies the exact language-neutral Comis protocol pin.
package bundle

const (
	// DigestAlgorithm is the manifest's language-neutral bundle hash contract.
	DigestAlgorithm = "sha256 over lexically ordered path, NUL, hash, newline records"
	// SourceProtocolPath is the only Comis subtree accepted by protocol sync.
	SourceProtocolPath = "packages/capability-service-sdk/protocol"
)

// Artifact is one manifest-owned protocol file and its SHA-256 digest.
type Artifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// GeneratorIdentity identifies the Comis generator that emitted the pin.
type GeneratorIdentity struct {
	Command string `json:"command"`
	Package string `json:"package"`
	Version string `json:"version"`
}

// ErrorDefinition is one closed JSON-RPC error contract.
type ErrorDefinition struct {
	Code      int    `json:"code"`
	Kind      string `json:"kind"`
	Retryable bool   `json:"retryable"`
}

// Limits records the exact bounded transport and retention values.
type Limits struct {
	MaxEvidenceBytes    int `json:"maxEvidenceBytes"`
	MaxInFlightRequests int `json:"maxInFlightRequests"`
	MaxLineBytes        int `json:"maxLineBytes"`
	MaxReportBytes      int `json:"maxReportBytes"`
	MaxRequestBytes     int `json:"maxRequestBytes"`
	MaxResponseBytes    int `json:"maxResponseBytes"`
	ReportRetentionDays int `json:"reportRetentionDays"`
}

// MCPMetadata names the private request and result extension keys.
type MCPMetadata struct {
	CallContextKey      string `json:"callContextKey"`
	ManagedRunResultKey string `json:"managedRunResultKey"`
}

// Method describes one closed control-plane call.
type Method struct {
	CallerClass          string   `json:"callerClass"`
	Classification       string   `json:"classification"`
	Direction            string   `json:"direction"`
	MaxRequestBytes      int      `json:"maxRequestBytes"`
	MaxResponseBytes     int      `json:"maxResponseBytes"`
	Name                 string   `json:"method"`
	OperationIDRequired  bool     `json:"operationIdRequired"`
	RequestSchema        string   `json:"requestSchema"`
	RequiredServiceScope *string  `json:"requiredServiceScope"`
	ResponseSchema       string   `json:"responseSchema"`
	SemanticInvariants   []string `json:"semanticInvariants"`
}

// Manifest is the strict source-of-truth inventory for the pinned bundle.
type Manifest struct {
	Artifacts             []Artifact        `json:"artifacts"`
	BundleDigest          string            `json:"bundleDigest"`
	BundleDigestAlgorithm string            `json:"bundleDigestAlgorithm"`
	ErrorKinds            []string          `json:"errorKinds"`
	Errors                []ErrorDefinition `json:"errors"`
	FixtureDigestToken    string            `json:"fixtureDigestToken"`
	Generator             GeneratorIdentity `json:"generator"`
	Limits                Limits            `json:"limits"`
	MCPMeta               MCPMetadata       `json:"mcpMeta"`
	MethodCatalog         []Method          `json:"methodCatalog"`
	Methods               []string          `json:"methods"`
	ProtocolID            string            `json:"protocolId"`
}

// Provenance ties copied bytes to one immutable Comis source commit.
type Provenance struct {
	SourceRepository   string            `json:"sourceRepository"`
	SourceCommit       string            `json:"sourceCommit"`
	SourceProtocolPath string            `json:"sourceProtocolPath"`
	ProtocolID         string            `json:"protocolId"`
	BundleDigest       string            `json:"bundleDigest"`
	Generator          GeneratorIdentity `json:"generator"`
}

// Bundle is a verified protocol directory.
type Bundle struct {
	Root     string
	Manifest Manifest
}

// PinnedBundle is a verified bundle whose provenance matches its manifest.
type PinnedBundle struct {
	Bundle
	Provenance Provenance
}
