package git

import (
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const maximumServerRebaseProofBytes = 1600000

func serverRebaseProofPath(
	repository Repository,
	request application.IntegrationAdapterRequest,
) (string, string, error) {
	digest := strings.TrimPrefix(integrationRebaseProofRef(request), "refs/heads/comis-integration-proof-")
	if len(digest) != 64 || strings.ContainsAny(digest, "/\\\x00\r\n\t ") {
		return "", "", errors.New("apply integration candidate: server rebase proof identity is invalid")
	}
	directory := filepath.Join(repository.WorktreeRoot, ".comis-integration-proofs")
	return directory, filepath.Join(directory, digest), nil
}

func ensureServerRebaseProofDirectory(worktreeRoot, directory string) error {
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(directory, 0o700); err != nil {
			return errors.New("apply integration candidate: server rebase proof directory could not be created")
		}
		if err := syncDirectory(worktreeRoot); err != nil {
			return err
		}
		info, err = os.Lstat(directory)
	}
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("apply integration candidate: server rebase proof directory is invalid")
	}
	return nil
}

func publishInitialServerRebaseProof(directory, path string, proof serverRebaseProof) error {
	contents := encodeServerRebaseProof(proof)
	temporary := path + ".pending"
	if err := stageServerRebaseProof(temporary, contents); err != nil {
		return err
	}
	if existing, found, err := readServerRebaseProof(path); err != nil {
		return err
	} else if found {
		if !sameServerRebaseProof(existing, proof) {
			return errors.New("apply integration candidate: server rebase proof differs")
		}
		if err := discardServerRebaseProofTemporary(temporary); err != nil {
			return err
		}
		return syncDirectory(directory)
	}
	if err := os.Rename(temporary, path); err != nil {
		return errors.New("apply integration candidate: server rebase proof could not be published")
	}
	return syncDirectory(directory)
}

func replaceServerRebaseProof(
	directory string,
	path string,
	existing serverRebaseProof,
	want serverRebaseProof,
) error {
	contents := encodeServerRebaseProof(want)
	temporary := path + ".next"
	if err := stageServerRebaseProof(temporary, contents); err != nil {
		return err
	}
	current, found, err := readServerRebaseProof(path)
	if err != nil || !found || !sameServerRebaseProof(current, existing) {
		return errors.New("apply integration candidate: server rebase proof changed before publication")
	}
	if err := os.Rename(temporary, path); err != nil {
		return errors.New("apply integration candidate: server rebase proof could not be completed")
	}
	return syncDirectory(directory)
}

func stageServerRebaseProof(path string, contents []byte) error {
	proof, found, err := readServerRebaseProof(path)
	if err == nil && found {
		if string(encodeServerRebaseProof(proof)) != string(contents) {
			return errors.New("apply integration candidate: pending server rebase proof differs")
		}
		return nil
	}
	if err != nil {
		if discardErr := discardServerRebaseProofTemporary(path); discardErr != nil {
			return discardErr
		}
	}
	return createServerRebaseProof(path, contents)
}

func discardServerRebaseProofTemporary(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return errors.New("apply integration candidate: pending server rebase proof is invalid")
	}
	if err := os.Remove(path); err != nil {
		return errors.New("apply integration candidate: pending server rebase proof could not be discarded")
	}
	return nil
}

func createServerRebaseProof(path string, contents []byte) error {
	if len(contents) == 0 || len(contents) > maximumServerRebaseProofBytes {
		return errors.New("apply integration candidate: server rebase proof exceeds its bound")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("apply integration candidate: server rebase proof could not be created")
	}
	offset := 0
	var writeErr error
	for offset < len(contents) && writeErr == nil {
		var written int
		written, writeErr = file.Write(contents[offset:])
		if written <= 0 && writeErr == nil {
			writeErr = io.ErrShortWrite
		}
		offset += written
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || offset != len(contents) || syncErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return errors.New("apply integration candidate: server rebase proof could not be persisted")
	}
	return nil
}

func readServerRebaseProof(path string) (serverRebaseProof, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return serverRebaseProof{}, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 ||
		info.Size() < 1 || info.Size() > maximumServerRebaseProofBytes {
		return serverRebaseProof{}, false, errors.New("apply integration candidate: server rebase proof is invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return serverRebaseProof{}, false, errors.New("apply integration candidate: server rebase proof is unavailable")
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, maximumServerRebaseProofBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(contents) > maximumServerRebaseProofBytes {
		return serverRebaseProof{}, false, errors.New("apply integration candidate: server rebase proof is unavailable")
	}
	proof, err := decodeServerRebaseProof(contents)
	if err != nil {
		return serverRebaseProof{}, false, err
	}
	return proof, true, nil
}

func encodeServerRebaseProof(proof serverRebaseProof) []byte {
	var builder strings.Builder
	builder.WriteString("version 6\noperation ")
	builder.WriteString(proof.operationID)
	builder.WriteString("\ncandidates ")
	writeRebaseProofCommits(&builder, proof.candidateCommits)
	builder.WriteString("patches ")
	writeRebaseProofCommits(&builder, proof.candidatePatches)
	builder.WriteString("resolved ")
	writeRebaseProofCommits(&builder, proof.resolvedCommits)
	builder.WriteString("conflicts ")
	builder.WriteString(strconv.Itoa(len(proof.conflicts)))
	builder.WriteByte('\n')
	for _, conflict := range proof.conflicts {
		builder.WriteString(conflict.commit)
		builder.WriteByte(' ')
		builder.WriteString(conflict.indexDigest)
		builder.WriteByte(' ')
		if conflict.resolvedTree == "" {
			builder.WriteString("- -")
		} else {
			builder.WriteString(conflict.resolvedTree)
			builder.WriteByte(' ')
			if conflict.expectedResult == "" {
				builder.WriteByte('-')
			} else {
				builder.WriteString(conflict.expectedResult)
			}
		}
		builder.WriteByte(' ')
		builder.WriteString(strconv.Itoa(len(conflict.paths)))
		builder.WriteByte('\n')
		for _, path := range conflict.paths {
			builder.WriteString(base64.RawURLEncoding.EncodeToString([]byte(path)))
			builder.WriteByte('\n')
		}
	}
	builder.WriteString("continued ")
	writeRebaseProofCommits(&builder, proof.continuedCommits)
	builder.WriteString("results ")
	writeRebaseProofCommits(&builder, proof.resultCommits)
	builder.WriteString("result ")
	if proof.resultingHead == "" {
		builder.WriteByte('-')
	} else {
		builder.WriteString(proof.resultingHead)
	}
	builder.WriteByte('\n')
	return []byte(builder.String())
}

func writeRebaseProofCommits(builder *strings.Builder, commits []string) {
	builder.WriteString(strconv.Itoa(len(commits)))
	builder.WriteByte('\n')
	for _, commit := range commits {
		builder.WriteString(commit)
		builder.WriteByte('\n')
	}
}

func decodeServerRebaseProof(contents []byte) (serverRebaseProof, error) {
	if len(contents) == 0 || contents[len(contents)-1] != '\n' {
		return serverRebaseProof{}, errors.New("apply integration candidate: server rebase proof is malformed")
	}
	lines := strings.Split(strings.TrimSuffix(string(contents), "\n"), "\n")
	if len(lines) < 9 || lines[0] != "version 6" || !strings.HasPrefix(lines[1], "operation ") {
		return serverRebaseProof{}, errors.New("apply integration candidate: server rebase proof is malformed")
	}
	operationID := strings.TrimPrefix(lines[1], "operation ")
	if domain.ValidateOperationID(operationID) != nil {
		return serverRebaseProof{}, errors.New("apply integration candidate: server rebase proof is malformed")
	}
	position := 2
	candidates, next, err := decodeRebaseProofCommits(lines, position, "candidates", 1, maximumRebaseProofCommits)
	if err != nil {
		return serverRebaseProof{}, err
	}
	patches, next, err := decodeRebaseProofPatches(lines, next, len(candidates))
	if err != nil {
		return serverRebaseProof{}, err
	}
	resolved, next, err := decodeRebaseProofCommits(lines, next, "resolved", 0, len(candidates))
	if err != nil {
		return serverRebaseProof{}, err
	}
	conflicts, next, err := decodeServerRebaseConflicts(lines, next, len(candidates))
	if err != nil || !validServerRebaseConflictBindings(resolved, conflicts) {
		return serverRebaseProof{}, errors.New("apply integration candidate: server rebase proof is malformed")
	}
	continued, next, err := decodeRebaseProofCommits(lines, next, "continued", 0, len(candidates))
	if err != nil {
		return serverRebaseProof{}, err
	}
	results, next, err := decodeRebaseProofCommits(lines, next, "results", 0, len(candidates))
	if err != nil || next != len(lines)-1 || !strings.HasPrefix(lines[next], "result ") {
		return serverRebaseProof{}, errors.New("apply integration candidate: server rebase proof is malformed")
	}
	result := strings.TrimPrefix(lines[next], "result ")
	proof := serverRebaseProof{
		operationID:      operationID,
		candidateCommits: candidates, candidatePatches: patches,
		resolvedCommits: resolved, conflicts: conflicts, continuedCommits: continued, resultCommits: results,
	}
	if result == "-" {
		if len(results) != 0 {
			return serverRebaseProof{}, errors.New("apply integration candidate: server rebase proof is malformed")
		}
		return proof, nil
	}
	if !gitRevisionPattern.MatchString(result) || len(results) != len(candidates) || results[len(results)-1] != result ||
		!sameRebaseCommits(continued, results[:len(continued)]) {
		return serverRebaseProof{}, errors.New("apply integration candidate: server rebase proof is malformed")
	}
	proof.resultingHead = result
	return proof, nil
}

func decodeRebaseProofPatches(lines []string, position int, expected int) ([]string, int, error) {
	if position >= len(lines) || !strings.HasPrefix(lines[position], "patches ") {
		return nil, position, errors.New("apply integration candidate: server rebase proof is malformed")
	}
	count, err := strconv.Atoi(strings.TrimPrefix(lines[position], "patches "))
	if err != nil || count != expected || position+count >= len(lines) {
		return nil, position, errors.New("apply integration candidate: server rebase proof is malformed")
	}
	patches := append([]string(nil), lines[position+1:position+1+count]...)
	for _, patch := range patches {
		if patch != "-" && !gitRevisionPattern.MatchString(patch) {
			return nil, position, errors.New("apply integration candidate: server rebase proof is malformed")
		}
	}
	return patches, position + count + 1, nil
}

func decodeRebaseProofCommits(
	lines []string,
	position int,
	label string,
	minimum int,
	maximum int,
) ([]string, int, error) {
	if position >= len(lines) || !strings.HasPrefix(lines[position], label+" ") {
		return nil, position, errors.New("apply integration candidate: server rebase proof is malformed")
	}
	count, err := strconv.Atoi(strings.TrimPrefix(lines[position], label+" "))
	if err != nil || count < minimum || count > maximum || position+count >= len(lines) {
		return nil, position, errors.New("apply integration candidate: server rebase proof is malformed")
	}
	commits := append([]string(nil), lines[position+1:position+1+count]...)
	seen := make(map[string]struct{}, len(commits))
	for _, commit := range commits {
		if !gitRevisionPattern.MatchString(commit) {
			return nil, position, errors.New("apply integration candidate: server rebase proof is malformed")
		}
		if _, duplicate := seen[commit]; duplicate {
			return nil, position, errors.New("apply integration candidate: server rebase proof is malformed")
		}
		seen[commit] = struct{}{}
	}
	return commits, position + count + 1, nil
}

func decodeServerRebaseConflicts(
	lines []string,
	position int,
	maximum int,
) ([]serverRebaseConflict, int, error) {
	if position >= len(lines) || !strings.HasPrefix(lines[position], "conflicts ") {
		return nil, position, errors.New("apply integration candidate: server rebase proof is malformed")
	}
	count, err := strconv.Atoi(strings.TrimPrefix(lines[position], "conflicts "))
	if err != nil || count < 0 || count > maximum {
		return nil, position, errors.New("apply integration candidate: server rebase proof is malformed")
	}
	position++
	conflicts := make([]serverRebaseConflict, 0, count)
	for range count {
		if position >= len(lines) {
			return nil, position, errors.New("apply integration candidate: server rebase proof is malformed")
		}
		fields := strings.Fields(lines[position])
		position++
		if len(fields) != 5 || !gitRevisionPattern.MatchString(fields[0]) ||
			!gitRevisionPattern.MatchString(fields[1]) {
			return nil, position, errors.New("apply integration candidate: server rebase proof is malformed")
		}
		resolvedTree, expectedResult := fields[2], fields[3]
		if resolvedTree == "-" {
			if expectedResult != "-" {
				return nil, position, errors.New("apply integration candidate: server rebase proof is malformed")
			}
			resolvedTree, expectedResult = "", ""
		} else if !gitRevisionPattern.MatchString(resolvedTree) {
			return nil, position, errors.New("apply integration candidate: server rebase proof is malformed")
		} else if expectedResult == "-" {
			expectedResult = ""
		} else if !gitRevisionPattern.MatchString(expectedResult) {
			return nil, position, errors.New("apply integration candidate: server rebase proof is malformed")
		}
		pathCount, err := strconv.Atoi(fields[4])
		if err != nil || pathCount < 1 || pathCount > 256 || position+pathCount > len(lines) {
			return nil, position, errors.New("apply integration candidate: server rebase proof is malformed")
		}
		paths := make([]string, 0, pathCount)
		for _, encoded := range lines[position : position+pathCount] {
			decoded, err := base64.RawURLEncoding.DecodeString(encoded)
			path := string(decoded)
			if err != nil || path == "" || len(decoded) > 1024 || strings.ContainsAny(path, "\x00\r\n") {
				return nil, position, errors.New("apply integration candidate: server rebase proof is malformed")
			}
			paths = append(paths, path)
		}
		position += pathCount
		conflicts = append(conflicts, serverRebaseConflict{
			commit: fields[0], indexDigest: fields[1], resolvedTree: resolvedTree,
			expectedResult: expectedResult, paths: paths,
		})
	}
	return conflicts, position, nil
}

func sameServerRebaseProofIdentity(left, right serverRebaseProof) bool {
	return left.operationID == right.operationID &&
		sameRebaseCommits(left.candidateCommits, right.candidateCommits) &&
		sameRebaseCommits(left.candidatePatches, right.candidatePatches)
}

func sameServerRebaseProof(left, right serverRebaseProof) bool {
	return left.operationID == right.operationID &&
		sameRebaseCommits(left.candidateCommits, right.candidateCommits) &&
		sameRebaseCommits(left.candidatePatches, right.candidatePatches) &&
		sameRebaseCommits(left.resolvedCommits, right.resolvedCommits) &&
		sameServerRebaseConflicts(left.conflicts, right.conflicts) &&
		sameRebaseCommits(left.continuedCommits, right.continuedCommits) &&
		sameRebaseCommits(left.resultCommits, right.resultCommits) && left.resultingHead == right.resultingHead
}

func sameRebaseCommits(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func containsRebaseCommit(commits []string, want string) bool {
	for _, commit := range commits {
		if commit == want {
			return true
		}
	}
	return false
}

func appendResolvedRebaseCommit(candidates, resolved []string, addition string) []string {
	wanted := make(map[string]struct{}, len(resolved)+1)
	for _, commit := range resolved {
		wanted[commit] = struct{}{}
	}
	wanted[addition] = struct{}{}
	ordered := make([]string, 0, len(wanted))
	for _, candidate := range candidates {
		if _, found := wanted[candidate]; found {
			ordered = append(ordered, candidate)
		}
	}
	return ordered
}

func validResolvedRebaseCommits(candidates, resolved []string) bool {
	return sameRebaseCommits(appendResolvedRebaseCommit(candidates, resolved, ""), resolved)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return errors.New("apply integration candidate: server rebase proof directory is unavailable")
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil || closeErr != nil {
		return errors.New("apply integration candidate: server rebase proof directory could not be persisted")
	}
	return nil
}
