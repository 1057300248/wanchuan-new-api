package architecture

import (
	"go/parser"
	goscanner "go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const modulePath = "github.com/QuantumNous/new-api"

var frontendConcreteChuanAPIBaseline = map[string]struct{}{
	"web/src/routes/help/index.tsx":        {},
	"web/src/routes/help/guides/$slug.tsx": {},
	"web/src/routes/help/api/index.tsx":    {},
}

var coreBrandLiteralBaseline = map[string]struct{}{
	"controller/misc.go":            {},
	"middleware/turnstile-check.go": {},
	"web/src/lib/public-site.ts":    {},
}

func TestGoCoreDoesNotImportConcreteChuanAPI(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()
	var offenders []string

	require.NoError(t, filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if skipDir(root, path) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		rel := slashRel(t, root, path)
		if rel == "main.go" || strings.HasPrefix(rel, "chuanapi/") || strings.HasPrefix(rel, "modules/chuanapi/") {
			return nil
		}

		file, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}
		for _, imp := range file.Imports {
			importPath, unquoteErr := strconv.Unquote(imp.Path.Value)
			if unquoteErr != nil {
				return unquoteErr
			}
			if importPath == modulePath+"/chuanapi" || strings.HasPrefix(importPath, modulePath+"/chuanapi/") ||
				importPath == modulePath+"/modules/chuanapi" || strings.HasPrefix(importPath, modulePath+"/modules/chuanapi/") {
				offenders = append(offenders, rel+" -> "+importPath)
			}
		}
		return nil
	}))

	assert.Empty(t, offenders, "core production Go code must not import concrete ChuanAPI modules; main.go is the composition-root exception")
}

func TestFrontendConcreteChuanAPIImportsDoNotGrow(t *testing.T) {
	root := repoRoot(t)
	webRoot := filepath.Join(root, "web", "src")
	actual := map[string]struct{}{}
	var unexpected []string

	require.NoError(t, filepath.WalkDir(webRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if filepath.Base(path) == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !(strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".tsx")) ||
			strings.Contains(filepath.Base(path), ".test.") || strings.Contains(filepath.Base(path), ".spec.") {
			return nil
		}

		rel := slashRel(t, root, path)
		if strings.HasPrefix(rel, "web/src/extensions/chuanapi/") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if !frontendConcreteImport.Match(data) {
			return nil
		}
		actual[rel] = struct{}{}
		if _, allowed := frontendConcreteChuanAPIBaseline[rel]; !allowed {
			unexpected = append(unexpected, rel)
		}
		return nil
	}))

	assert.Empty(t, unexpected, "Batch A freezes the current three frontend concrete imports; new offenders are forbidden")
	assert.LessOrEqual(t, len(actual), len(frontendConcreteChuanAPIBaseline), "frontend concrete ChuanAPI imports may shrink but must not grow beyond the Batch A baseline")
}

func TestCoreChuanAPIDomainStringLiteralsDoNotGrow(t *testing.T) {
	root := repoRoot(t)
	var offenders []string

	require.NoError(t, filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if skipDir(root, path) {
				return filepath.SkipDir
			}
			return nil
		}
		rel := slashRel(t, root, path)
		if strings.HasPrefix(rel, "chuanapi/") || strings.HasPrefix(rel, "modules/chuanapi/") ||
			strings.HasPrefix(rel, "web/src/extensions/chuanapi/") || strings.HasPrefix(rel, "docs/") || strings.HasPrefix(rel, "ops/") ||
			strings.HasSuffix(rel, "_test.go") || strings.Contains(filepath.Base(rel), ".test.") || strings.Contains(filepath.Base(rel), ".spec.") {
			return nil
		}

		hasLiteral, scanErr := hasChuanAPIDomainLiteral(path)
		if scanErr != nil {
			return scanErr
		}
		if !hasLiteral {
			return nil
		}
		if _, allowed := coreBrandLiteralBaseline[rel]; !allowed {
			offenders = append(offenders, rel)
		}
		return nil
	}))

	assert.Empty(t, offenders, "Batch A freezes current production chuanapi.com literals; Phase 1 will drive the allowlist to zero")
}

func hasChuanAPIDomainLiteral(path string) (bool, error) {
	switch {
	case strings.HasSuffix(path, ".go"):
		data, err := os.ReadFile(path)
		if err != nil {
			return false, err
		}
		fset := token.NewFileSet()
		file := fset.AddFile(path, fset.Base(), len(data))
		var scanner goscanner.Scanner
		scanner.Init(file, data, nil, 0)
		for {
			_, tok, lit := scanner.Scan()
			if tok == token.EOF {
				return false, nil
			}
			if tok != token.STRING {
				continue
			}
			value, unquoteErr := strconv.Unquote(lit)
			if unquoteErr == nil && strings.Contains(value, "chuanapi.com") {
				return true, nil
			}
		}
	case strings.HasSuffix(path, ".ts"), strings.HasSuffix(path, ".tsx"):
		data, err := os.ReadFile(path)
		if err != nil {
			return false, err
		}
		return quotedDomainLiteral.Match(data), nil
	default:
		return false, nil
	}
}

var frontendConcreteImport = regexp.MustCompile(`(?m)(from\s*["']@/extensions/chuanapi(?:/[^"']*)?["']|import\s*\(\s*["']@/extensions/chuanapi(?:/[^"']*)?["']\s*\))`)

var quotedDomainLiteral = regexp.MustCompile("[\\\"'`]([^\\\"'`]|\\\\.)*chuanapi\\.com([^\\\"'`]|\\\\.)*[\\\"'`]")

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "could not locate repository root from %s", dir)
		dir = parent
	}
}

func skipDir(root, path string) bool {
	if path == root {
		return false
	}
	name := filepath.Base(path)
	return name == ".git" || name == "node_modules" || name == "vendor" || name == "dist" || name == ".codex-worktrees"
}

func slashRel(t *testing.T, root, path string) string {
	t.Helper()
	rel, err := filepath.Rel(root, path)
	require.NoError(t, err)
	return filepath.ToSlash(rel)
}
