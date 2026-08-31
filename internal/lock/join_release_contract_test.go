package lock

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// JoinRelease only reaches the caller when its result is assigned to a named
// result of the enclosing function: a deferred assignment to a local error is
// evaluated after the return values were already copied, so a failed release
// would be dropped while looking handled. errcheck cannot see that.
func TestJoinReleaseAssignsANamedResult(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if name := entry.Name(); name == ".git" || name == "bin" || name == "dist" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		checkJoinReleaseFile(t, path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func checkJoinReleaseFile(t *testing.T, path string) {
	t.Helper()
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		checkJoinReleaseScope(t, fileSet, function.Name.Name, function.Type, function.Body, nil)
	}
}

// checkJoinReleaseScope inspects one function body against the named results
// visible there. A function literal that declares its own named results
// shadows the enclosing ones; a deferred `func() { ... }()` declares none and
// captures the enclosing function's results, which is the intended pattern.
func checkJoinReleaseScope(t *testing.T, fileSet *token.FileSet, owner string, signature *ast.FuncType, body *ast.BlockStmt, enclosing map[string]struct{}) {
	t.Helper()
	named := make(map[string]struct{}, len(enclosing))
	for name := range enclosing {
		named[name] = struct{}{}
	}
	if signature.Results != nil && len(signature.Results.List) != 0 && len(signature.Results.List[0].Names) != 0 {
		named = make(map[string]struct{})
		for _, field := range signature.Results.List {
			for _, name := range field.Names {
				named[name.Name] = struct{}{}
			}
		}
	}
	ast.Inspect(body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.FuncLit:
			checkJoinReleaseScope(t, fileSet, owner+" literal", value.Type, value.Body, named)
			return false
		case *ast.AssignStmt:
			if len(value.Rhs) != 1 || !isJoinReleaseCall(value.Rhs[0]) {
				return true
			}
			target, ok := value.Lhs[0].(*ast.Ident)
			if !ok {
				t.Errorf("%s: JoinRelease result must be assigned to a named result", fileSet.Position(value.Pos()))
				return true
			}
			if _, isNamed := named[target.Name]; !isNamed {
				t.Errorf("%s: JoinRelease result is assigned to %q, which is not a named result of %s", fileSet.Position(value.Pos()), target.Name, owner)
			}
		}
		return true
	})
}

func isJoinReleaseCall(expression ast.Expr) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return false
	}
	switch callee := call.Fun.(type) {
	case *ast.Ident:
		return callee.Name == "JoinRelease"
	case *ast.SelectorExpr:
		return callee.Sel.Name == "JoinRelease"
	}
	return false
}
