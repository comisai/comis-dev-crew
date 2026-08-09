package architecture_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var productCommands = []string{"devcrew", "devcrew-mcp", "devcrew-report", "devcrew-service"}

func TestArchitecture_ProductCompositionRootsRemainExactlyFour(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join(repositoryRoot(t), "cmd"))
	if err != nil {
		t.Fatalf("read command roots: %v", err)
	}
	var actual []string
	for _, entry := range entries {
		if entry.IsDir() {
			actual = append(actual, entry.Name())
		}
	}
	sort.Strings(actual)
	if strings.Join(actual, ",") != strings.Join(productCommands, ",") {
		t.Fatalf("product command roots = %v, want %v", actual, productCommands)
	}
	if _, err := os.Stat(filepath.Join(repositoryRoot(t), "package.json")); !os.IsNotExist(err) {
		t.Fatalf("repository must not introduce a Node.js runtime manifest")
	}
}

func TestArchitecture_ProductionSourcesRespectAuthorityBoundaries(t *testing.T) {
	root := repositoryRoot(t)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if relative == ".git" || relative == "tools" || relative == "test" || relative == "coverage" || relative == "dist" || relative == "bin" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		checkProductionFile(t, root, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk production source: %v", err)
	}
}

func TestArchitecture_SourceFilesStayWithinReviewableSizePolicy(t *testing.T) {
	root := repositoryRoot(t)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "coverage" || entry.Name() == "dist" || entry.Name() == "bin" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(contents[:min(len(contents), 256)]), "Code generated") {
			return nil
		}
		limit := 500
		if strings.HasSuffix(path, "_test.go") {
			limit = 800
		}
		lines := strings.Count(string(contents), "\n") + 1
		if lines > limit {
			t.Errorf("%s has %d lines, limit is %d", path, lines, limit)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk source size policy: %v", err)
	}
}

func checkProductionFile(t *testing.T, root, filePath string) {
	t.Helper()
	relative, err := filepath.Rel(root, filePath)
	if err != nil {
		t.Fatalf("relative path for %s: %v", filePath, err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), filePath, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", relative, err)
	}
	imports := make(map[string]string)
	for _, imported := range parsed.Imports {
		importPath, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			t.Fatalf("decode import in %s: %v", relative, err)
		}
		name := filepath.Base(importPath)
		if imported.Name != nil {
			name = imported.Name.Name
		}
		imports[name] = importPath
		checkImportBoundary(t, filepath.ToSlash(relative), importPath)
	}
	ast.Inspect(parsed, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch function := call.Fun.(type) {
		case *ast.Ident:
			if function.Name == "panic" {
				t.Errorf("%s: production code must not panic for expected failures", relative)
			}
		case *ast.SelectorExpr:
			qualifier, ok := function.X.(*ast.Ident)
			if !ok {
				return true
			}
			importPath := imports[qualifier.Name]
			if importPath == "os" && (function.Sel.Name == "Getenv" || function.Sel.Name == "LookupEnv") && !strings.HasPrefix(filepath.ToSlash(relative), "cmd/") && !strings.Contains(filepath.ToSlash(relative), "/config/") {
				t.Errorf("%s: environment reads belong in configuration adapters or composition roots", relative)
			}
			if importPath == "os" && function.Sel.Name == "Exit" && !strings.HasPrefix(filepath.ToSlash(relative), "cmd/") {
				t.Errorf("%s: os.Exit is allowed only in cmd composition roots", relative)
			}
		}
		return true
	})
}

func checkImportBoundary(t *testing.T, relative, importPath string) {
	t.Helper()
	const module = "github.com/comisai/comis-dev-crew/"
	if strings.HasPrefix(relative, "internal/domain/") && strings.HasPrefix(importPath, module) {
		t.Errorf("%s: domain package must import only the standard library", relative)
	}
	if strings.HasPrefix(relative, "internal/application/") && strings.HasPrefix(importPath, module+"internal/") && !strings.HasPrefix(importPath, module+"internal/domain") {
		t.Errorf("%s: application may import only domain from internal packages", relative)
	}
	if strings.HasPrefix(relative, "internal/localapi/") && strings.HasPrefix(importPath, module+"internal/") &&
		!strings.HasPrefix(importPath, module+"internal/application") && !strings.HasPrefix(importPath, module+"internal/domain") {
		t.Errorf("%s: local API may import only application and domain from internal packages", relative)
	}
	if strings.HasPrefix(relative, "internal/cli/") && strings.HasPrefix(importPath, module+"internal/") &&
		!strings.HasPrefix(importPath, module+"internal/application") && !strings.HasPrefix(importPath, module+"internal/domain") &&
		!strings.HasPrefix(importPath, module+"internal/localapi") {
		t.Errorf("%s: CLI may import only application, domain, and the typed local client from internal packages", relative)
	}
	if strings.HasPrefix(relative, "internal/mcpadapter/") && strings.HasPrefix(importPath, module+"internal/") &&
		!strings.HasPrefix(importPath, module+"internal/application") && !strings.HasPrefix(importPath, module+"internal/comiswire") &&
		!strings.HasPrefix(importPath, module+"internal/domain") && !strings.HasPrefix(importPath, module+"internal/localapi") {
		t.Errorf("%s: MCP facade may import only application, Comis wire DTOs, domain, and the typed local client", relative)
	}
	if strings.HasPrefix(importPath, module+"internal/store/sqlite") && relative != "internal/service/service.go" {
		t.Errorf("%s: only the service composition may import the writable SQLite adapter", relative)
	}
	if relative == "cmd/devcrew/main.go" && strings.HasPrefix(importPath, module+"internal/") {
		allowed := []string{module + "internal/cli", module + "internal/command", module + "internal/localapi", module + "internal/localconfig"}
		isAllowed := false
		for _, allowedImport := range allowed {
			if importPath == allowedImport {
				isAllowed = true
				break
			}
		}
		if !isAllowed {
			t.Errorf("%s: operator CLI composition must reach domain behavior only through the local client", relative)
		}
	}
	if importPath == "database/sql" && !strings.HasPrefix(relative, "internal/store/sqlite/") {
		t.Errorf("%s: database/sql belongs only in the SQLite store adapter", relative)
	}
	if strings.Contains(importPath, "modernc.org/sqlite") && !strings.HasPrefix(relative, "internal/store/sqlite/") {
		t.Errorf("%s: SQLite driver belongs only in the SQLite store adapter", relative)
	}
	if importPath == "os/exec" {
		allowed := []string{"internal/git/", "internal/workers/", "internal/forge/", "internal/processes/", "internal/validation/"}
		for _, prefix := range allowed {
			if strings.HasPrefix(relative, prefix) {
				return
			}
		}
		t.Errorf("%s: process execution belongs only in reviewed process adapters", relative)
	}
}
