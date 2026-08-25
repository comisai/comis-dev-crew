package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/application"
)

const (
	maximumIntegrationReceiptBytes = 2048
	maximumPackedRefsBytes         = 16 << 20
)

type integrationReceiptSnapshot map[string]inspectedIntegrationReceipt

func (registry *Registry) integrationReceiptFamilySnapshot(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
) (integrationReceiptSnapshot, error) {
	references := integrationReceiptFamilyReferences(request)
	return registry.integrationReceiptSnapshot(ctx, request.Target.WorktreePath, references)
}

func integrationReceiptFamilyReferences(request application.IntegrationAdapterRequest) []string {
	identities := []application.IntegrationAdapterRequest{request}
	if request.RecoveryOperationID != "" {
		identities = append(identities, originalIntegrationRequest(request))
	}
	references := make([]string, 0, len(identities)*5)
	for _, identity := range identities {
		for _, outcome := range []string{"applied", "conflicted", "rebased", "target"} {
			references = append(references, integrationReceiptRef(outcome, identity))
		}
		references = append(references, integrationRebaseProofRef(identity))
	}
	sort.Strings(references)
	unique := references[:0]
	for _, reference := range references {
		if len(unique) == 0 || unique[len(unique)-1] != reference {
			unique = append(unique, reference)
		}
	}
	return unique
}

func (registry *Registry) integrationReceiptSnapshot(
	ctx context.Context,
	worktreePath string,
	references []string,
) (integrationReceiptSnapshot, error) {
	if ctx == nil {
		return nil, errors.New("apply integration candidate: receipt context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	repository, err := registry.integrationRepositoryForPath(worktreePath)
	if err != nil {
		return nil, err
	}
	first, err := readIntegrationReceiptSnapshot(repository.GitCommonDir, references)
	if err != nil {
		return nil, err
	}
	second, err := readIntegrationReceiptSnapshot(repository.GitCommonDir, references)
	if err != nil || !sameIntegrationReceiptSnapshot(first, second) {
		return nil, errors.New("apply integration candidate: receipt family changed during inspection")
	}
	return first, nil
}

func (registry *Registry) integrationRepositoryForPath(worktreePath string) (Repository, error) {
	for _, repository := range registry.repositories {
		if worktreePath == repository.PrimaryCheckout || pathWithin(repository.WorktreeRoot, worktreePath, true) {
			return repository, nil
		}
	}
	return Repository{}, errors.New("apply integration candidate: receipt repository is unavailable")
}

func readIntegrationReceiptSnapshot(
	commonDirectory string,
	references []string,
) (snapshot integrationReceiptSnapshot, returnErr error) {
	root, err := os.OpenRoot(commonDirectory)
	if err != nil {
		return nil, errors.New("apply integration candidate: receipt store is unavailable")
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	wanted := make(map[string]struct{}, len(references))
	for _, reference := range references {
		if !validIntegrationReceiptReference(reference) {
			return nil, errors.New("apply integration candidate: receipt reference is invalid")
		}
		if _, duplicate := wanted[reference]; duplicate {
			return nil, errors.New("apply integration candidate: receipt reference is duplicated")
		}
		wanted[reference] = struct{}{}
	}
	packed, err := readPackedIntegrationReceipts(root, wanted)
	if err != nil {
		return nil, err
	}
	snapshot = make(integrationReceiptSnapshot, len(references))
	for _, reference := range references {
		receipt, found, err := readLooseIntegrationReceipt(root, reference)
		if err != nil {
			return nil, err
		}
		if !found {
			receipt = packed[reference]
		}
		snapshot[reference] = receipt
	}
	return snapshot, nil
}

func validIntegrationReceiptReference(reference string) bool {
	if path.Clean(reference) != reference || strings.ContainsAny(reference, "\\\x00\r\n\t ") {
		return false
	}
	return strings.HasPrefix(reference, "refs/comis/integration/") ||
		strings.HasPrefix(reference, "refs/heads/comis-integration-proof-")
}

func readLooseIntegrationReceipt(root *os.Root, reference string) (inspectedIntegrationReceipt, bool, error) {
	if err := validateIntegrationReceiptParents(root, reference); err != nil {
		return inspectedIntegrationReceipt{}, false, err
	}
	info, err := root.Lstat(reference)
	if errors.Is(err, os.ErrNotExist) {
		return inspectedIntegrationReceipt{kind: integrationReceiptAbsent}, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() < 2 || info.Size() > maximumIntegrationReceiptBytes {
		return inspectedIntegrationReceipt{}, false, errors.New("apply integration candidate: loose receipt is invalid")
	}
	file, err := root.Open(reference)
	if err != nil {
		return inspectedIntegrationReceipt{}, false, errors.New("apply integration candidate: loose receipt is unavailable")
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, maximumIntegrationReceiptBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(contents) > maximumIntegrationReceiptBytes ||
		!bytes.HasSuffix(contents, []byte{'\n'}) || bytes.Count(contents, []byte{'\n'}) != 1 {
		return inspectedIntegrationReceipt{}, false, errors.New("apply integration candidate: loose receipt is malformed")
	}
	value := string(bytes.TrimSuffix(contents, []byte{'\n'}))
	if strings.HasPrefix(value, "ref: ") {
		target := strings.TrimPrefix(value, "ref: ")
		if !strings.HasPrefix(target, "refs/") || path.Clean(target) != target ||
			strings.ContainsAny(target, "\\\x00\r\n\t ") {
			return inspectedIntegrationReceipt{}, false, errors.New("apply integration candidate: symbolic receipt is invalid")
		}
		return inspectedIntegrationReceipt{kind: integrationReceiptSymbolic, value: target}, true, nil
	}
	if !gitRevisionPattern.MatchString(value) {
		return inspectedIntegrationReceipt{}, false, errors.New("apply integration candidate: direct receipt is invalid")
	}
	return inspectedIntegrationReceipt{kind: integrationReceiptDirect, value: value}, true, nil
}

func validateIntegrationReceiptParents(root *os.Root, reference string) error {
	parents := make([]string, 0, 5)
	for parent := path.Dir(reference); parent != "."; parent = path.Dir(parent) {
		parents = append(parents, parent)
	}
	for index := len(parents) - 1; index >= 0; index-- {
		info, err := root.Lstat(parents[index])
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("apply integration candidate: receipt namespace is invalid")
		}
	}
	return nil
}

func readPackedIntegrationReceipts(
	root *os.Root,
	wanted map[string]struct{},
) (map[string]inspectedIntegrationReceipt, error) {
	receipts := make(map[string]inspectedIntegrationReceipt)
	info, err := root.Lstat("packed-refs")
	if errors.Is(err, os.ErrNotExist) {
		return receipts, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() < 1 || info.Size() > maximumPackedRefsBytes {
		return nil, errors.New("apply integration candidate: packed receipts are invalid")
	}
	file, err := root.Open("packed-refs")
	if err != nil {
		return nil, errors.New("apply integration candidate: packed receipts are unavailable")
	}
	defer file.Close()
	reader := bufio.NewReaderSize(io.LimitReader(file, maximumPackedRefsBytes+1), 4096)
	total := 0
	for {
		line, readErr := reader.ReadString('\n')
		total += len(line)
		if total > maximumPackedRefsBytes {
			return nil, errors.New("apply integration candidate: packed receipts exceed their bound")
		}
		if len(line) != 0 {
			if line[len(line)-1] != '\n' {
				return nil, errors.New("apply integration candidate: packed receipts are malformed")
			}
			line = strings.TrimSuffix(line, "\n")
			if line != "" && line[0] != '#' && line[0] != '^' {
				fields := strings.Split(line, " ")
				if len(fields) != 2 || !gitRevisionPattern.MatchString(fields[0]) ||
					!strings.HasPrefix(fields[1], "refs/") || path.Clean(fields[1]) != fields[1] {
					return nil, errors.New("apply integration candidate: packed receipts are malformed")
				}
				if _, requested := wanted[fields[1]]; requested {
					if _, duplicate := receipts[fields[1]]; duplicate {
						return nil, errors.New("apply integration candidate: packed receipt is duplicated")
					}
					receipts[fields[1]] = inspectedIntegrationReceipt{kind: integrationReceiptDirect, value: fields[0]}
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, errors.New("apply integration candidate: packed receipts are unavailable")
		}
	}
	return receipts, nil
}

func sameIntegrationReceiptSnapshot(left, right integrationReceiptSnapshot) bool {
	if len(left) != len(right) {
		return false
	}
	for reference, receipt := range left {
		if right[reference] != receipt {
			return false
		}
	}
	return true
}

func requireSnapshotReceipt(
	snapshot integrationReceiptSnapshot,
	reference string,
	kind integrationReceiptKind,
	value string,
) error {
	receipt, found := snapshot[reference]
	if !found || receipt.kind != kind || kind != integrationReceiptAbsent && receipt.value != value {
		return errors.New("integration receipt differs")
	}
	return nil
}
