package reporter

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPublishRuntimeDirectoryRefusesOccupiedDestination(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	staged := filepath.Join(root, "staged")
	occupied := filepath.Join(root, "published")
	if err := os.Mkdir(staged, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(occupied, 0o700); err != nil {
		t.Fatal(err)
	}
	expected := runtimePathTestIdentity(t, staged)
	directory := runtimePathTestDirectoryDescriptor(t, root)
	_, err := PublishRuntimeDirectoryIdentity(directory, "staged", "published", expected, 0o700)
	if !errors.Is(err, ErrRuntimePathIdentity) {
		t.Fatalf("PublishRuntimeDirectoryIdentity(occupied destination) error = %v", err)
	}
	if _, statErr := os.Lstat(staged); statErr != nil {
		t.Fatalf("staged directory after refused publication error = %v", statErr)
	}
}

func TestVerifyPublishedRuntimeDirectoryRejectsForeignAndDriftedIdentity(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	published := filepath.Join(root, "published")
	foreign := filepath.Join(root, "foreign")
	if err := os.Mkdir(published, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(foreign, 0o700); err != nil {
		t.Fatal(err)
	}
	expected := runtimePathTestIdentity(t, published)
	directory := runtimePathTestDirectoryDescriptor(t, root)
	pin, err := pinExpectedRuntimePath(directory, "published", expected, RuntimePathDirectory, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closeRuntimeRemovalPin(pin) }()

	if _, err := verifyPublishedRuntimeDirectory(directory, "foreign", pin, expected, 0o700); !errors.Is(err, ErrRuntimePathIdentity) {
		t.Fatalf("verifyPublishedRuntimeDirectory(foreign name) error = %v", err)
	}
	drifted := expected
	drifted.Inode++
	if _, err := verifyPublishedRuntimeDirectory(directory, "published", pin, drifted, 0o700); !errors.Is(err, ErrRuntimePathIdentity) {
		t.Fatalf("verifyPublishedRuntimeDirectory(drifted identity) error = %v", err)
	}
}

func TestPublishRuntimePathReleasesPinWhenPreconditionFails(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	staged := filepath.Join(root, "record.new")
	if err := os.WriteFile(staged, []byte("prepared"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected := runtimePathTestIdentity(t, staged)
	directory := runtimePathTestDirectoryDescriptor(t, root)
	refusal := errors.New("publication precondition is unmet")
	err := publishRuntimePath(directory, "record.new", "record", expected, 0o600, func() error { return refusal })
	if !errors.Is(err, refusal) {
		t.Fatalf("publishRuntimePath(refused precondition) error = %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(root, "record")); !os.IsNotExist(statErr) {
		t.Fatalf("destination after refused publication error = %v, want absent", statErr)
	}
}

func TestReplaceRuntimePathRefusesDriftedSource(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	staged := filepath.Join(root, "record.new")
	destination := filepath.Join(root, "record")
	if err := os.WriteFile(staged, []byte("prepared"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("prior"), 0o600); err != nil {
		t.Fatal(err)
	}
	stagedIdentity := runtimePathTestIdentity(t, staged)
	stagedIdentity.Inode++
	destinationIdentity := runtimePathTestIdentity(t, destination)
	directory := runtimePathTestDirectoryDescriptor(t, root)
	err := replaceRuntimePath(directory, "record.new", "record", stagedIdentity, destinationIdentity, 0o600, nil)
	if !errors.Is(err, ErrRuntimePathIdentity) {
		t.Fatalf("replaceRuntimePath(drifted source) error = %v", err)
	}
}

func TestReplaceRuntimePathReleasesPinsWhenPreconditionFails(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	staged := filepath.Join(root, "record.new")
	destination := filepath.Join(root, "record")
	if err := os.WriteFile(staged, []byte("prepared"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("prior"), 0o600); err != nil {
		t.Fatal(err)
	}
	directory := runtimePathTestDirectoryDescriptor(t, root)
	refusal := errors.New("replacement precondition is unmet")
	err := replaceRuntimePath(
		directory, "record.new", "record",
		runtimePathTestIdentity(t, staged), runtimePathTestIdentity(t, destination), 0o600,
		func() error { return refusal },
	)
	if !errors.Is(err, refusal) {
		t.Fatalf("replaceRuntimePath(refused precondition) error = %v", err)
	}
	contents, readErr := os.ReadFile(destination)
	if readErr != nil || string(contents) != "prior" {
		t.Fatalf("destination after refused replacement = %q, %v", contents, readErr)
	}
}

func TestReplaceRuntimePathReportsUnexchangeableMapping(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	staged := filepath.Join(root, "record.new")
	destination := filepath.Join(root, "record")
	if err := os.WriteFile(staged, []byte("prepared"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("prior"), 0o600); err != nil {
		t.Fatal(err)
	}
	directory := runtimePathTestDirectoryDescriptor(t, root)
	err := replaceRuntimePath(
		directory, "record.new", "record",
		runtimePathTestIdentity(t, staged), runtimePathTestIdentity(t, destination), 0o600,
		func() error { return os.Remove(destination) },
	)
	if !errors.Is(err, ErrRuntimePathIdentity) {
		t.Fatalf("replaceRuntimePath(absent destination) error = %v", err)
	}
	if contents, readErr := os.ReadFile(staged); readErr != nil || string(contents) != "prepared" {
		t.Fatalf("preserved source after refused exchange = %q, %v", contents, readErr)
	}
}

func TestRuntimePathMutationFailureKeepsUnrelatedCausesDistinguishable(t *testing.T) {
	cause := errors.New("device is unavailable")
	err := runtimePathMutationFailure("runtime path publication source differs", cause)
	if !errors.Is(err, cause) {
		t.Fatalf("runtimePathMutationFailure() lost its cause: %v", err)
	}
	if errors.Is(err, ErrRuntimePathIdentity) {
		t.Fatalf("runtimePathMutationFailure() classified an unrelated cause as an identity conflict: %v", err)
	}
}

func TestPublishAndReplaceRuntimePathCompleteExactMappings(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	staged := filepath.Join(root, "record.new")
	destination := filepath.Join(root, "record")
	if err := os.WriteFile(staged, []byte("published"), 0o600); err != nil {
		t.Fatal(err)
	}
	directory := runtimePathTestDirectoryDescriptor(t, root)
	if err := PublishRuntimePath(
		directory, "record.new", "record", runtimePathTestIdentity(t, staged), 0o600,
	); err != nil {
		t.Fatalf("PublishRuntimePath() error = %v", err)
	}
	contents, readErr := os.ReadFile(destination)
	if readErr != nil || string(contents) != "published" {
		t.Fatalf("published destination = %q, %v", contents, readErr)
	}
	if _, statErr := os.Lstat(staged); !os.IsNotExist(statErr) {
		t.Fatalf("source after publication error = %v, want absent", statErr)
	}

	replacement := filepath.Join(root, "record.next")
	if err := os.WriteFile(replacement, []byte("replaced"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceRuntimePath(
		directory, "record.next", "record",
		runtimePathTestIdentity(t, replacement), runtimePathTestIdentity(t, destination), 0o600,
	); err != nil {
		t.Fatalf("ReplaceRuntimePath() error = %v", err)
	}
	destinationContents, destinationErr := os.ReadFile(destination)
	exchangedContents, exchangedErr := os.ReadFile(replacement)
	if destinationErr != nil || exchangedErr != nil ||
		string(destinationContents) != "replaced" || string(exchangedContents) != "published" {
		t.Fatalf("exchanged mappings = %q/%q, %v/%v",
			destinationContents, exchangedContents, destinationErr, exchangedErr)
	}
}
