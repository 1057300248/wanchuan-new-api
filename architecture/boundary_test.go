package architecture

import (
	"go/ast"
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

var frontendConcreteChuanAPIBaseline = map[string]struct{}{}

var coreBrandLiteralBaseline = map[string]struct{}{}

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

func TestServiceProductionDoesNotOwnWalletFunding(t *testing.T) {
	root := repoRoot(t)
	serviceRoot := filepath.Join(root, "service")
	fset := token.NewFileSet()
	var offenders []string

	require.NoError(t, filepath.WalkDir(serviceRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(file, func(node ast.Node) bool {
			typeSpec, ok := node.(*ast.TypeSpec)
			if ok && typeSpec.Name != nil && typeSpec.Name.Name == "WalletFunding" {
				offenders = append(offenders, slashRel(t, root, path))
			}
			return true
		})
		return nil
	}))

	assert.Empty(t, offenders, "production service must depend on moneyport funding capabilities, not own WalletFunding")
}

func TestAsyncImageOwnershipStaysInGenericModule(t *testing.T) {
	root := repoRoot(t)
	for _, legacy := range []string{
		"controller/image_async.go",
		"service/image_async.go",
	} {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(legacy)))
		assert.ErrorIs(t, err, os.ErrNotExist, "%s must not return to a root business package", legacy)
	}

	relayRouter, err := os.ReadFile(filepath.Join(root, "router", "relay-router.go"))
	require.NoError(t, err)
	assert.NotContains(t, string(relayRouter), "/v1/images/tasks",
		"async image task routes must stay owned by modules/imagegen")
}

func TestChuanAPICompositionRootStaysThin(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "chuanapi", "register.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	require.NoError(t, err)

	var offenders []string
	for _, imp := range file.Imports {
		importPath, unquoteErr := strconv.Unquote(imp.Path.Value)
		require.NoError(t, unquoteErr)
		if importPath == "fmt" || importPath == modulePath+"/extension" || importPath == modulePath+"/pkg/moneyport" || importPath == modulePath+"/pkg/paymentport" || strings.HasPrefix(importPath, modulePath+"/modules/chuanapi/") {
			continue
		}
		offenders = append(offenders, importPath)
	}
	assert.Empty(t, offenders, "chuanapi/register.go must remain a thin composition root: no controller/model/service/common business imports")
}

func TestCoreApiRouterDoesNotOwnWaffoRoutes(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "router", "api-router.go"))
	require.NoError(t, err)
	assert.NotContains(t, strings.ToLower(string(data)), "waffo", "Waffo route ownership belongs to modules/chuanapi/payment, not the upstream-hot core API router")
}

func TestPaymentProviderOwnershipStaysInModule(t *testing.T) {
	root := repoRoot(t)
	for _, legacy := range []string{
		"controller/topup_waffo.go",
		"controller/topup_waffo_pancake.go",
		"controller/subscription_payment_waffo_pancake.go",
		"service/waffo_pancake.go",
	} {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(legacy)))
		assert.ErrorIs(t, err, os.ErrNotExist, "%s must remain module-owned", legacy)
	}

	for _, coreFile := range []string{"controller/topup.go", "controller/payment_webhook_availability.go"} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(coreFile)))
		require.NoError(t, err)
		assert.NotContains(t, strings.ToLower(string(data)), "waffo",
			"generic payment catalogue/availability code must not know concrete Waffo providers: %s", coreFile)
	}

	moduleRoot := filepath.Join(root, "modules", "chuanapi", "payment")
	fset := token.NewFileSet()
	var offenders []string
	require.NoError(t, filepath.WalkDir(moduleRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			importPath, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			if importPath == modulePath+"/controller" || strings.HasPrefix(importPath, modulePath+"/controller/") ||
				importPath == modulePath+"/service" || strings.HasPrefix(importPath, modulePath+"/service/") {
				offenders = append(offenders, slashRel(t, root, path)+" -> "+importPath)
			}
		}
		return nil
	}))
	assert.Empty(t, offenders, "ChuanAPI payment module must own provider handlers/services without importing root controller/service")
}

func TestChuanAPIRouteModulesUseHostOwnedAPIScopes(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range []string{
		"modules/chuanapi/payment/register.go",
		"modules/chuanapi/wallet/routes/register.go",
	} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		require.NoError(t, err)
		source := string(data)
		assert.Contains(t, source, "RegisterScoped:", "%s must consume host route scopes", rel)
		for _, forbidden := range []string{
			`Group("/api")`,
			"gzip.Gzip(",
			"AccessTokenAudit()",
			"BodyStorageCleanup()",
			"GlobalAPIRateLimit()",
		} {
			assert.NotContains(t, source, forbidden,
				"%s must not reconstruct host API middleware policy (%s)", rel, forbidden)
		}
	}
}

func TestPaymentProviderCompositionUsesRegistry(t *testing.T) {
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
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), "paymentport.SetDefault") {
			offenders = append(offenders, slashRel(t, root, path))
		}
		return nil
	}))
	assert.Empty(t, offenders, "payment providers must register through the frozen multi-provider registry")
}

func TestWalletAndAffiliateRuntimeOwnershipStaysInModules(t *testing.T) {
	root := repoRoot(t)
	for _, legacy := range []string{
		"controller/wallet.go",
		"service/affiliate_reward_maintenance.go",
	} {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(legacy)))
		assert.ErrorIs(t, err, os.ErrNotExist, "%s must remain module-owned", legacy)
	}

	apiRouter, err := os.ReadFile(filepath.Join(root, "router", "api-router.go"))
	require.NoError(t, err)
	for _, path := range []string{
		`"/wallet"`,
		`"/wallet/ledger"`,
		`"/referral/overview"`,
		`"/referral/rewards"`,
	} {
		assert.NotContains(t, string(apiRouter), path,
			"Wallet/Affiliate user route %s belongs to modules/chuanapi/wallet/routes", path)
	}

	moduleRoot := filepath.Join(root, "modules", "chuanapi", "affiliate")
	fset := token.NewFileSet()
	var offenders []string
	require.NoError(t, filepath.WalkDir(moduleRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			importPath, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			if importPath == modulePath+"/controller" || strings.HasPrefix(importPath, modulePath+"/controller/") ||
				importPath == modulePath+"/service" || strings.HasPrefix(importPath, modulePath+"/service/") {
				offenders = append(offenders, slashRel(t, root, path)+" -> "+importPath)
			}
		}
		return nil
	}))
	assert.Empty(t, offenders, "Affiliate module must not import root controller/service")
}

func TestWalletCommercialPolicyDoesNotLeakBackIntoCore(t *testing.T) {
	root := repoRoot(t)
	apiRouter, err := os.ReadFile(filepath.Join(root, "router", "api-router.go"))
	require.NoError(t, err)
	assert.NotContains(t, string(apiRouter), "bonus_eligible", "wallet commercial admin routes belong to modules/chuanapi/wallet")

	_, err = os.Stat(filepath.Join(root, "controller", "model_bonus_eligible.go"))
	assert.ErrorIs(t, err, os.ErrNotExist, "wallet commercial admin handler must not return to root controller ownership")

	billingRoot := filepath.Join(root, "pkg", "billingexpr")
	fset := token.NewFileSet()
	var offenders []string
	require.NoError(t, filepath.WalkDir(billingRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			importPath, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			if importPath == modulePath+"/chuanapi" || strings.HasPrefix(importPath, modulePath+"/modules/chuanapi/") {
				offenders = append(offenders, slashRel(t, root, path)+" -> "+importPath)
			}
		}
		return nil
	}))
	assert.Empty(t, offenders, "generic billing expression engine must not depend on ChuanAPI commercial policy")
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

	assert.Empty(t, unexpected, "Phase 2 removed frontend concrete ChuanAPI imports from core; new offenders are forbidden")
	assert.Empty(t, actual, "frontend core must consume ChuanAPI UI only through generic extension accessors")
}

func TestFrontendCompositionRootStaysIsolated(t *testing.T) {
	root := repoRoot(t)
	indexPath := filepath.Join(root, "web", "src", "extensions", "index.ts")
	bootstrapPath := filepath.Join(root, "web", "src", "extensions", "bootstrap.ts")

	index, err := os.ReadFile(indexPath)
	require.NoError(t, err)
	assert.NotContains(t, string(index), "./chuanapi",
		"generic extension API must not import concrete ChuanAPI composition")

	bootstrap, err := os.ReadFile(bootstrapPath)
	require.NoError(t, err)
	assert.Contains(t, string(bootstrap), "from './chuanapi'",
		"frontend composition root must explicitly wire the concrete ChuanAPI extension")

	mainEntry, err := os.ReadFile(filepath.Join(root, "web", "src", "main.tsx"))
	require.NoError(t, err)
	assert.Contains(t, string(mainEntry), "./extensions/bootstrap",
		"application entrypoint must initialize frontend composition before rendering")

	var offenders []string
	webRoot := filepath.Join(root, "web", "src")
	require.NoError(t, filepath.WalkDir(webRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !(strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".tsx")) {
			return nil
		}
		rel := slashRel(t, root, path)
		if rel == "web/src/extensions/bootstrap.ts" || strings.HasPrefix(rel, "web/src/extensions/chuanapi/") ||
			strings.Contains(filepath.Base(rel), ".test.") || strings.Contains(filepath.Base(rel), ".spec.") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), "from './chuanapi'") || strings.Contains(string(data), "from \"./chuanapi\"") {
			offenders = append(offenders, rel)
		}
		return nil
	}))
	assert.Empty(t, offenders, "only web/src/extensions/bootstrap.ts may wire the concrete frontend ChuanAPI extension")
}

func TestWalletFrontendOwnershipStaysInChuanAPIExtension(t *testing.T) {
	root := repoRoot(t)
	legacy := filepath.Join(root, "web", "src", "features", "wallet")
	_, err := os.Stat(legacy)
	assert.ErrorIs(t, err, os.ErrNotExist, "Wallet frontend must remain extension-owned")

	route, err := os.ReadFile(filepath.Join(root, "web", "src", "routes", "_authenticated", "wallet", "index.tsx"))
	require.NoError(t, err)
	assert.NotContains(t, string(route), "@/features/wallet")
	assert.Contains(t, string(route), "extensionWalletPage")
}

func TestPaymentSettingsFrontendOwnershipStaysInChuanAPIExtension(t *testing.T) {
	root := repoRoot(t)
	for _, legacy := range []string{
		"web/src/features/system-settings/integrations/payment-settings-section.tsx",
		"web/src/features/system-settings/integrations/payment-method-dialog.tsx",
		"web/src/features/system-settings/integrations/payment-methods-visual-editor.tsx",
		"web/src/features/system-settings/integrations/waffo-settings-section.tsx",
		"web/src/features/system-settings/integrations/waffo-pancake-settings-section.tsx",
		"web/src/features/system-settings/integrations/waffo-pancake-api.ts",
	} {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(legacy)))
		assert.ErrorIs(t, err, os.ErrNotExist, "%s must remain ChuanAPI extension-owned", legacy)
	}

	registry, err := os.ReadFile(filepath.Join(root, "web", "src", "features", "system-settings", "billing", "section-registry.tsx"))
	require.NoError(t, err)
	assert.Contains(t, string(registry), "extensionPaymentSettingsSection")
	assert.NotContains(t, strings.ToLower(string(registry)), "waffo",
		"generic billing section registry must not know concrete Waffo providers")
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

	assert.Empty(t, offenders, "Phase 1 removed production core chuanapi.com literals; concrete brand domains must stay in configuration or ChuanAPI modules")
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
