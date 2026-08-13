// Package installer_test exercises docs/install.sh against a fake release
// endpoint. The script is the only supported binary distribution path, so its
// digest verification and refusal behavior need the same gate as any other
// boundary in this repository.
package installer_test

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const testVersion = "v0.1.0"

var commands = []string{"devcrew", "devcrew-service", "devcrew-mcp", "devcrew-report"}

func scriptPath(t *testing.T) string {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve installer test source path")
	}
	return filepath.Join(filepath.Dir(sourceFile), "..", "..", "docs", "install.sh")
}

// archiveName mirrors the name the script derives from uname output.
func archiveName(t *testing.T) string {
	t.Helper()
	switch runtime.GOARCH {
	case "amd64", "arm64":
	default:
		t.Skipf("installer supports amd64 and arm64, not %s", runtime.GOARCH)
	}
	return fmt.Sprintf("comis-dev-crew-%s-%s-%s.tar.gz", testVersion, runtime.GOOS, runtime.GOARCH)
}

// buildArchive writes a release-shaped archive holding one stub per command
// under bin/, matching the layout tools/smoke already verifies.
func buildArchive(t *testing.T, path string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	zip := gzip.NewWriter(file)
	defer zip.Close()
	archive := tar.NewWriter(zip)
	defer archive.Close()

	for _, name := range commands {
		body := "#!/bin/sh\necho " + name + " " + testVersion + "\n"
		header := &tar.Header{
			Name: "bin/" + name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}
		if err := archive.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
}

func digestOf(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents))
}

// fakeCurl builds a curl stand-in that serves the release endpoints from files,
// so the test drives the real script without reaching the network.
func fakeCurl(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	body := `#!/bin/sh
url=""
out=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift ;;
    -*) ;;
    *) url="$1" ;;
  esac
  shift
done
case "$url" in
  *releases/latest) src="$FAKE_RELEASE_JSON" ;;
  *releases?per_page=1|*releases\?per_page=1) src="$FAKE_RELEASE_LIST" ;;
  *checksums.txt) src="$FAKE_CHECKSUMS" ;;
  *.tar.gz) src="$FAKE_ARCHIVE" ;;
  *) exit 22 ;;
esac
if [ -z "$src" ] || [ ! -f "$src" ]; then exit 22; fi
if [ -n "$out" ]; then cp "$src" "$out"; else cat "$src"; fi
`
	path := filepath.Join(directory, "curl")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return directory
}

type release struct {
	home      string
	linkDir   string
	archive   string
	checksums string
	json      string
	list      string
	curlDir   string
}

// newRelease stages a complete, internally consistent fake release.
func newRelease(t *testing.T) *release {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("installer supports darwin and linux, not %s", runtime.GOOS)
	}
	serve := t.TempDir()
	name := archiveName(t)
	staged := &release{
		home:      t.TempDir(),
		linkDir:   filepath.Join(t.TempDir(), "link"),
		archive:   filepath.Join(serve, name),
		checksums: filepath.Join(serve, "checksums.txt"),
		json:      filepath.Join(serve, "latest.json"),
		list:      filepath.Join(serve, "list.json"),
		curlDir:   fakeCurl(t),
	}
	buildArchive(t, staged.archive)
	staged.writeChecksums(t, digestOf(t, staged.archive))
	body := fmt.Sprintf(`{"tag_name": %q, "name": %q}`, testVersion, testVersion)
	if err := os.WriteFile(staged.json, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	list := fmt.Sprintf(`[{"tag_name": %q, "prerelease": true}]`, testVersion)
	if err := os.WriteFile(staged.list, []byte(list), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(staged.linkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return staged
}

func (staged *release) writeChecksums(t *testing.T, digest string) {
	t.Helper()
	line := fmt.Sprintf("%s  %s\n", digest, filepath.Base(staged.archive))
	if err := os.WriteFile(staged.checksums, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (staged *release) run(t *testing.T) (string, error) {
	t.Helper()
	command := exec.Command("sh", scriptPath(t))
	command.Env = append(os.Environ(),
		"HOME="+staged.home,
		"PATH="+staged.curlDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DEVCREW_LINK_DIR="+staged.linkDir,
		"FAKE_RELEASE_JSON="+staged.json,
		"FAKE_RELEASE_LIST="+staged.list,
		"FAKE_CHECKSUMS="+staged.checksums,
		"FAKE_ARCHIVE="+staged.archive,
	)
	output, err := command.CombinedOutput()
	return string(output), err
}

func (staged *release) installDir() string {
	return filepath.Join(staged.home, ".comis-dev-crew", "bin")
}

func TestInstallScript_InstallsEveryCommandAndLinksItOntoPath(t *testing.T) {
	staged := newRelease(t)

	output, err := staged.run(t)
	if err != nil {
		t.Fatalf("install.sh error = %v\n%s", err, output)
	}
	if !strings.Contains(output, "Checksum verified.") {
		t.Errorf("install.sh did not report checksum verification\n%s", output)
	}

	for _, name := range commands {
		installed := filepath.Join(staged.installDir(), name)
		info, statErr := os.Stat(installed)
		if statErr != nil {
			t.Fatalf("stat %s: %v", installed, statErr)
		}
		if info.Mode().Perm() != 0o755 {
			t.Errorf("%s mode = %v, want 0755", name, info.Mode().Perm())
		}
		target, linkErr := os.Readlink(filepath.Join(staged.linkDir, name))
		if linkErr != nil {
			t.Fatalf("readlink %s: %v", name, linkErr)
		}
		if target != installed {
			t.Errorf("%s link -> %s, want %s", name, target, installed)
		}
	}
}

func TestInstallScript_RefusesAMismatchedDigestAndInstallsNothing(t *testing.T) {
	staged := newRelease(t)
	staged.writeChecksums(t, strings.Repeat("0", 64))

	output, err := staged.run(t)
	if err == nil {
		t.Fatalf("install.sh accepted a mismatched digest\n%s", output)
	}
	if !strings.Contains(output, "checksum mismatch") {
		t.Errorf("install.sh did not name the mismatch\n%s", output)
	}
	assertNothingInstalled(t, staged)
}

func TestInstallScript_RefusesAnArchiveAbsentFromTheChecksums(t *testing.T) {
	staged := newRelease(t)
	if err := os.WriteFile(staged.checksums, []byte("# no entries\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	output, err := staged.run(t)
	if err == nil {
		t.Fatalf("install.sh accepted an unlisted archive\n%s", output)
	}
	if !strings.Contains(output, "no entry for") {
		t.Errorf("install.sh did not report the missing entry\n%s", output)
	}
	assertNothingInstalled(t, staged)
}

func TestInstallScript_RefusesWhenTheChecksumsCannotBeFetched(t *testing.T) {
	staged := newRelease(t)
	if err := os.Remove(staged.checksums); err != nil {
		t.Fatal(err)
	}

	output, err := staged.run(t)
	if err == nil {
		t.Fatalf("install.sh installed without checksums\n%s", output)
	}
	if !strings.Contains(output, "Refusing to install unverified binaries") {
		t.Errorf("install.sh did not refuse explicitly\n%s", output)
	}
	assertNothingInstalled(t, staged)
}

func TestInstallScript_ExplainsWhenNoReleaseIsPublished(t *testing.T) {
	staged := newRelease(t)
	if err := os.WriteFile(staged.json, []byte(`{"message": "Not Found"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged.list, []byte(`[]`), 0o600); err != nil {
		t.Fatal(err)
	}

	output, err := staged.run(t)
	if err == nil {
		t.Fatalf("install.sh continued without a release\n%s", output)
	}
	if !strings.Contains(output, "could not determine the latest release") {
		t.Errorf("install.sh did not explain the missing release\n%s", output)
	}
	if !strings.Contains(output, "DEVCREW_VERSION") {
		t.Errorf("install.sh did not offer the exact-tag override\n%s", output)
	}
	assertNothingInstalled(t, staged)
}

func assertNothingInstalled(t *testing.T, staged *release) {
	t.Helper()
	for _, name := range commands {
		if _, err := os.Lstat(filepath.Join(staged.installDir(), name)); !os.IsNotExist(err) {
			t.Errorf("%s was installed despite a refused download", name)
		}
		if _, err := os.Lstat(filepath.Join(staged.linkDir, name)); !os.IsNotExist(err) {
			t.Errorf("%s was linked despite a refused download", name)
		}
	}
}

// GitHub answers releases/latest with 404 while every published release is a
// prerelease, which is this project's normal state. The installer must fall
// back to the release list rather than report that nothing is published.
func TestInstallScript_ResolvesAPrereleaseWhenLatestIsAbsent(t *testing.T) {
	staged := newRelease(t)
	if err := os.WriteFile(staged.json, []byte(`{"message": "Not Found"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	output, err := staged.run(t)
	if err != nil {
		t.Fatalf("install.sh did not fall back to the release list: %v\n%s", err, output)
	}
	for _, name := range commands {
		if _, statErr := os.Stat(filepath.Join(staged.installDir(), name)); statErr != nil {
			t.Errorf("%s was not installed from the prerelease: %v", name, statErr)
		}
	}
}
