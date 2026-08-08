package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

type moduleFile struct {
	name    string
	existed bool
	before  []byte
}

func main() {
	files := []moduleFile{capture("go.mod"), capture("go.sum")}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if output, err := exec.CommandContext(ctx, "go", "mod", "tidy").CombinedOutput(); err != nil {
		fail("go mod tidy", output, err)
	}
	for _, file := range files {
		after, err := os.ReadFile(file.name)
		if !file.existed && os.IsNotExist(err) {
			continue
		}
		if err != nil {
			fail(file.name, nil, err)
		}
		if !file.existed || !bytes.Equal(file.before, after) {
			fmt.Fprintf(os.Stderr, "%s changed after go mod tidy; run it and commit the result\n", file.name)
			os.Exit(1)
		}
	}
	if output, err := exec.CommandContext(ctx, "go", "mod", "verify").CombinedOutput(); err != nil {
		fail("go mod verify", output, err)
	}
	fmt.Println("module files are tidy and checksums are verified")
}

func capture(name string) moduleFile {
	contents, err := os.ReadFile(name)
	if os.IsNotExist(err) {
		return moduleFile{name: name}
	}
	if err != nil {
		fail(name, nil, err)
	}
	return moduleFile{name: name, existed: true, before: contents}
}

func fail(step string, output []byte, err error) {
	fmt.Fprintf(os.Stderr, "%s failed: %v\n%s", step, err, output)
	os.Exit(1)
}
