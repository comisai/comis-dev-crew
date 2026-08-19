package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestCLI_EveryTaskVerbTheParserAcceptsAppearsInUsage closes the class rather
// than one instance of it. A verb the parser accepts but the usage text never
// names is unreachable in practice: an operator reads `--help`, does not see
// the command, and concludes the service cannot do the thing it can already do.
// Asserting one known verb at a time only proves the verb somebody remembered.
//
// The accepted set is read from the dispatcher's own source so a verb added
// tomorrow is covered without anyone updating a list here.
func TestCLI_EveryTaskVerbTheParserAcceptsAppearsInUsage(t *testing.T) {
	verbs := taskVerbsAcceptedByParser(t)
	if len(verbs) == 0 {
		t.Fatal("no task verbs were read from the dispatcher source")
	}
	for _, verb := range verbs {
		if !strings.Contains(usage, "task "+verb) {
			t.Errorf("task verb %q is accepted by the parser but missing from the CLI usage text", verb)
		}
	}
}

// taskVerbsAcceptedByParser reads every literal compared against the task
// subcommand argument inside parseTaskCommand, covering both the early-return
// comparisons and the trailing switch.
func taskVerbsAcceptedByParser(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "cli.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse cli.go: %v", err)
	}
	var dispatcher *ast.FuncDecl
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == "parseTaskCommand" {
			dispatcher = function
			break
		}
	}
	if dispatcher == nil {
		t.Fatal("parseTaskCommand is no longer present in cli.go")
	}
	found := map[string]struct{}{}
	ast.Inspect(dispatcher.Body, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.BinaryExpr:
			if typed.Op == token.EQL && isTaskSubcommandArgument(typed.X) {
				addStringLiteral(found, typed.Y)
			}
		case *ast.SwitchStmt:
			if !isTaskSubcommandArgument(typed.Tag) {
				return true
			}
			for _, statement := range typed.Body.List {
				clause, ok := statement.(*ast.CaseClause)
				if !ok {
					continue
				}
				for _, expression := range clause.List {
					addStringLiteral(found, expression)
				}
			}
		}
		return true
	})
	verbs := make([]string, 0, len(found))
	for verb := range found {
		verbs = append(verbs, verb)
	}
	sort.Strings(verbs)
	return verbs
}

// isTaskSubcommandArgument matches the `args[0]` selector the dispatcher
// switches on, so an unrelated comparison never contributes a phantom verb.
func isTaskSubcommandArgument(expression ast.Expr) bool {
	index, ok := expression.(*ast.IndexExpr)
	if !ok {
		return false
	}
	identifier, ok := index.X.(*ast.Ident)
	if !ok || identifier.Name != "args" {
		return false
	}
	literal, ok := index.Index.(*ast.BasicLit)
	return ok && literal.Kind == token.INT && literal.Value == "0"
}

func addStringLiteral(into map[string]struct{}, expression ast.Expr) {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil || value == "" {
		return
	}
	into[value] = struct{}{}
}
