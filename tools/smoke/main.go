package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

var commands = []string{"devcrew-service", "devcrew", "devcrew-mcp", "devcrew-report"}

func main() {
	temporaryRoot, err := os.MkdirTemp("", "devcrew-smoke-")
	if err != nil {
		fatal("create temporary root", err)
	}
	defer os.RemoveAll(temporaryRoot)
	staging := filepath.Join(temporaryRoot, "staging")
	binDir := filepath.Join(staging, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		fatal("create staging bin", err)
	}
	for _, name := range commands {
		outputPath := filepath.Join(binDir, name)
		output, err := exec.Command("go", "build", "-trimpath", "-o", outputPath, "./cmd/"+name).CombinedOutput()
		if err != nil {
			fatal("build "+name, fmt.Errorf("%w: %s", err, output))
		}
	}
	for _, name := range []string{"LICENSE", "README.md"} {
		if err := copyFile(name, filepath.Join(staging, name)); err != nil {
			fatal("stage "+name, err)
		}
	}
	archivePath := filepath.Join(temporaryRoot, "comis-dev-crew.tar.gz")
	if err := createArchive(staging, archivePath); err != nil {
		fatal("create release archive", err)
	}
	installRoot := filepath.Join(temporaryRoot, "install")
	if err := extractArchive(archivePath, installRoot); err != nil {
		fatal("extract release archive", err)
	}
	for _, name := range commands {
		binary := filepath.Join(installRoot, "bin", name)
		for _, argument := range []string{"--version", "--help"} {
			output, err := exec.Command(binary, argument).CombinedOutput()
			if err != nil {
				fatal(name+" "+argument, fmt.Errorf("%w: %s", err, output))
			}
		}
	}
	fmt.Println("release-shaped archive and four-command help/version smoke passed")
}

func copyFile(source, destination string) error {
	contents, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(destination, contents, 0o644)
}

func createArchive(root, destination string) error {
	file, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipWriter := gzip.NewWriter(file)
	defer gzipWriter.Close()
	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()
	return filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name, err = filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		source, err := os.Open(path)
		if err != nil {
			return err
		}
		defer source.Close()
		_, err = io.Copy(tarWriter, source)
		return err
	})
}

func extractArchive(source, destination string) error {
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target := filepath.Join(destination, filepath.Clean(header.Name))
		if header.FileInfo().IsDir() {
			if err := os.MkdirAll(target, header.FileInfo().Mode().Perm()); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		destinationFile, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, header.FileInfo().Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(destinationFile, tarReader); err != nil {
			destinationFile.Close()
			return err
		}
		if err := destinationFile.Close(); err != nil {
			return err
		}
	}
}

func fatal(step string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", step, err)
	os.Exit(1)
}
