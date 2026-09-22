"""Apply reviewed post-merge fixes, once, only on the isolated rc40 branch.
Every replacement is exact. A mismatch aborts before any remote push.
"""
from pathlib import Path
import json
import os
import subprocess

if (os.environ.get('GITHUB_ACTIONS'), os.environ.get('GITHUB_REPOSITORY'), os.environ.get('GITHUB_REF')) != ('true', '1057300248/wanchuan-new-api', 'refs/heads/codex/rc40-full-integration-20260922'):
    raise SystemExit('Unexpected execution context')
marker = Path('.github/rc40/followup-applied.json')
if marker.exists():
    raise SystemExit(0)
subprocess.run(['git', 'merge-base', '--is-ancestor', 'a1ee61c51d727f74880d5570628e65f0c9b5177a', 'HEAD'], check=True)

def change(path, old, new, count=1):
    p = Path(path)
    text = p.read_text()
    if text.count(old) != count:
        raise RuntimeError(f'Unreviewed content in {path}: expected {count} occurrences of {old[:60]!r}')
    p.write_text(text.replace(old, new))

change('service/relay_error.go', '\tif ShouldSkipRetryAfterChannelAffinityFailure(c) {', '''	// A handled SSE failure must dominate channel-error retries and retry budgets.
	// Replaying would splice another response into the stream and can double bill.
	if c != nil && (common.GetContextKeyBool(c, constant.ContextKeyResponsesStreamErrorHandled) ||
		common.GetContextKeyBool(c, constant.ContextKeyStreamErrorHandled)) {
		return PolicyDecision{Action: "stop", Reason: "non_retryable_error", Source: "system"}
	}
	if ShouldSkipRetryAfterChannelAffinityFailure(c) {''')
change('controller/relay.go', '''func shouldRetry(c *gin.Context, openaiErr *types.NewAPIError, retryTimes int) bool {
	if common.GetContextKeyBool(c, constant.ContextKeyResponsesStreamErrorHandled) ||
		common.GetContextKeyBool(c, constant.ContextKeyStreamErrorHandled) {
		return false
	}
	return service.ShouldRetryRelayError(c, openaiErr, retryTimes)
}

''', '')
change('controller/relay_responses_stream_error_test.go', 'shouldRetry(c, apiErr, 3)', 'service.ShouldRetryRelayError(c, apiErr, 3)', 2)
change('controller/relay_responses_stream_error_test.go', '\t\tassert.False(t, service.ShouldRetryRelayError', '\t\tassert.Equal(t, "stop", service.DecideRelayRetry(c, apiErr, 3).Action)\n\t\tassert.False(t, service.ShouldRetryRelayError', 2)
change('service/relay_error_test.go', 'func TestRequestPolicyEventsReachLogAdminInfo(t *testing.T) {', '''func TestDecideRelayRetryNeverReplaysHandledStreams(t *testing.T) {
	for _, handled := range []constant.ContextKey{constant.ContextKeyResponsesStreamErrorHandled, constant.ContextKeyStreamErrorHandled} {
		for _, code := range []types.ErrorCode{types.ErrorCodeBadResponse, types.ErrorCodeChannelNoAvailableKey} {
			for _, budget := range []int{0, 3} {
				t.Run(fmt.Sprintf("%s/%s/%d", handled, code, budget), func(t *testing.T) {
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					common.SetContextKey(c, handled, true)
					err := types.NewOpenAIError(errors.New("partial stream already failed"), code, http.StatusBadGateway)
					assert.Equal(t, PolicyDecision{Action: "stop", Reason: "non_retryable_error", Source: "system"}, DecideRelayRetry(c, err, budget))
					assert.False(t, ShouldRetryRelayError(c, err, budget))
				})
			}
		}
	}
}

func TestRequestPolicyEventsReachLogAdminInfo(t *testing.T) {''')

p = 'controller/billing_option_test.go'
change(p, '"github.com/QuantumNous/new-api/model"', '"github.com/QuantumNous/new-api/model"\n\twalletmodule "github.com/QuantumNous/new-api/modules/chuanapi/wallet"\n\t"github.com/QuantumNous/new-api/pkg/moneyport"')
change(p, 'func TestPreConsumePolicyDatabaseMatrix(t *testing.T) {', '''func TestPreConsumePolicyDatabaseMatrix(t *testing.T) {
	registry := moneyport.NewFactoryRegistry()
	require.NoError(t, service.RegisterHostFundingFactories(registry))
	require.NoError(t, walletmodule.RegisterFundingFactory(registry))
	if err := service.ConfigureFundingFactoryRegistry(registry); err != nil {
		require.Contains(t, err.Error(), "already configured")
	}''')
change(p, 'db.AutoMigrate(&model.Token{})', 'db.AutoMigrate(&model.Token{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{})')
change(p, 'Quota: tc.wallet, Group: "default"', 'Quota: tc.wallet, QuotaPaid: tc.wallet, Group: "default"')
change(p, 'info := &relaycommon.RelayInfo{UserId: user.Id,', 'info := &relaycommon.RelayInfo{RequestId: fmt.Sprintf("preconsume-policy-%s-%d", dialect.kind, i), UserId: user.Id,')
for expression in ['tc.wallet-tc.wantHeld', 'tc.wallet-actual']:
    old = f'assert.Equal(t, {expression}, user.Quota)'
    change(p, old, old + '\n\t\t\t\t\tassert.Equal(t, user.Quota, user.QuotaPaid)\n\t\t\t\t\tassert.Zero(t, user.QuotaBonus)\n\t\t\t\t\tassert.Zero(t, user.QuotaReward)')

p = 'controller/plugin_protocol_image.go'
change(p, 'pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"', 'pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"\n\t"github.com/QuantumNous/new-api/pkg/privacy"')
change(p, '\tc.JSON(http.StatusOK, response)', '''	payloadJSON, err := common.Marshal(response)
	if err != nil {
		respondPluginProtocolError(c, http.StatusInternalServerError, "task_protocol_error", "Task protocol request failed")
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", privacy.SanitizeClientPayload(c, payloadJSON))''')
change(p, '// API. DashScope failure bodies arrive as JSON {code, message}; their message\n// is surfaced so a client sees the vendor reason instead of raw JSON.', '// API. Ordinary callers use the shared privacy boundary. Only an explicitly\n// marked administrator channel test receives provider diagnostics.')
change(p, '''	c.JSON(status, gin.H{"error": gin.H{
		"message": common.MessageWithRequestId(message, c.GetString(common.RequestIdKey)),
		"type":    errorType,
		"code":    code,
	}})''', '''	clientError := privacy.SanitizeClientOpenAIError(c, status, types.OpenAIError{
		Message: common.MessageWithRequestId(message, c.GetString(common.RequestIdKey)),
		Type: errorType,
		Code: code,
	})
	c.JSON(status, gin.H{"error": clientError})''')
change('pkg/privacy/allowlist.go', '\t"billing_hard_limit_reached": {},', '\t"billing_hard_limit_reached": {},\n\t"image_generation_failed": {},\n\t"task_timeout": {},\n\t"request_cancelled": {},')
p = 'controller/plugin_protocol_test.go'
change(p, '{"failure", model.TaskStatusFailure, http.StatusBadRequest, "image_generation_failed", "DataInspectionFailed"}', '{"failure", model.TaskStatusFailure, http.StatusBadRequest, "image_generation_failed", "invalid request"}')
change(p, '{"timeout", "", http.StatusGatewayTimeout, "task_timeout", "still running"}', '{"timeout", "", http.StatusGatewayTimeout, "task_timeout", "upstream request failed"}')
change(p, '// Submission failures use the OpenAI error envelope and surface a DashScope\n// {code, message} failure body as a readable vendor message.', '// Submission failures retain OpenAI envelopes without exposing provider diagnostics\n// to ordinary callers; channel-test disclosure is covered separately.')
change(p, 'assert.Equal(t, "InvalidParameter", errorBody["code"])', 'assert.Nil(t, errorBody["code"])')
change(p, 'assert.Contains(t, errorBody["message"], "n must be 1")', 'assert.Equal(t, "invalid request", errorBody["message"])\n\tassert.NotContains(t, recorder.Body.String(), "InvalidParameter")')
change(p, 'return {data: data, vendor: "test"};', 'return {data: data, vendor: "test", model: "private-provider-image-model"};')
change(p, 'c.Set("resolved_task_model", "image-model")', 'c.Set("resolved_task_model", "image-model")\n\tcommon.SetContextKey(c, constant.ContextKeyOriginalModel, "image-model")')
change(p, 'assert.Equal(t, "test", response["vendor"], "plugin fields are preserved")', 'assert.Equal(t, "test", response["vendor"], "plugin fields are preserved")\n\t\t\tassert.Equal(t, "image-model", response["model"])\n\t\t\tassert.NotContains(t, recorder.Body.String(), "private-provider-image-model")')
p = Path('controller/plugin_protocol_image_privacy_test.go')
assert not p.exists()
p.write_text('''package controller

import (
    "errors"
    "net/http"
    "testing"

    "github.com/QuantumNous/new-api/common"
    "github.com/QuantumNous/new-api/constant"
    "github.com/QuantumNous/new-api/service"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestTaskPluginImageErrorHonorsPrivacyBoundary(t *testing.T) {
    for _, role := range []int{common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser} {
        c, recorder := newImageProtocolTestContext("")
        c.Set("role", role)
        c.Set(common.RequestIdKey, "local-image-request")
        taskErr := service.TaskErrorWrapper(errors.New(`{"code":"private-vendor-code","message":"https://private.example/api?key=secret-key quota_account=private-account","request_id":"vendor-request"}`), "fail_to_fetch_task", http.StatusBadGateway)
        respondTaskPluginImageError(c, taskErr)
        require.Equal(t, http.StatusBadGateway, recorder.Code)
        assert.Contains(t, recorder.Body.String(), "upstream request failed")
        assert.Contains(t, recorder.Body.String(), "local-image-request")
        for _, secret := range []string{"private.example", "secret-key", "private-account", "private-vendor-code", "vendor-request"} {
            assert.NotContains(t, recorder.Body.String(), secret)
        }
    }
    c, recorder := newImageProtocolTestContext("")
    common.SetContextKey(c, constant.ContextKeyChannelTest, true)
    taskErr := service.TaskErrorWrapper(errors.New(`{"code":"InvalidParameter","message":"n must be 1"}`), "fail_to_fetch_task", http.StatusBadRequest)
    respondTaskPluginImageError(c, taskErr)
    require.Equal(t, http.StatusBadRequest, recorder.Code)
    assert.Contains(t, recorder.Body.String(), "InvalidParameter")
    assert.Contains(t, recorder.Body.String(), "n must be 1")
}
''')

p = 'web/src/features/keys/components/__tests__/api-addresses.test.tsx'
change(p, 'StatusData', 'StatusRecord', 2)
p = 'web/src/features/keys/components/dialogs/__tests__/cc-switch-dialog.test.tsx'
change(p, "import { CCSwitchDialog } from '../cc-switch-dialog'", '''import type { ApiKey, CCSwitchApp } from '../../../types'
import { CCSwitchDialog } from '../cc-switch-dialog'
import { ccSwitchModelsQueryKey } from '../cc-switch-utils'

const token: ApiKey = {
  id: 1, name: 'Scoped fixture', key: 'masked', status: 1,
  remain_quota: 0, used_quota: 0, unlimited_quota: true,
  expired_time: -1, created_time: 1, accessed_time: 1,
  group: 'default', auto_groups: ['default'], cross_group_retry: false,
  model_limits_enabled: false, model_limits: '', allow_ips: '',
}''')
change(p, """  queryClient.setQueryData(['user-models-ccswitch'], {
    success: true,
    data: models,
  })""", """  for (const app of ['claude', 'codex', 'gemini'] satisfies CCSwitchApp[]) {
    queryClient.setQueryData(ccSwitchModelsQueryKey(token, app), {
      success: true,
      data: models,
    })
  }""")
change(p, "<CCSwitchDialog open onOpenChange={vi.fn()} tokenKey='test-only' />", "<CCSwitchDialog open onOpenChange={vi.fn()} tokenKey='test-only' token={token} />")
change(p, "queryClient.setQueryData(['user-models-ccswitch'], {", "queryClient.setQueryData(ccSwitchModelsQueryKey(token, 'codex'), {")
p = 'web/src/features/system-settings/general/__tests__/pre-consume-settings.test.tsx'
change(p, '            QuotaForInviter: 0,\n            QuotaForInvitee: 0,', '            SignupBonusEnabled: false,\n            QuotaForSignupBonus: 0,\n            AffiliateRewardEnabled: false,\n            AffiliateRewardRate: 0,')
p = 'web/src/features/system-settings/models/__tests__/group-rename.test.tsx'
change(p, "import { GroupRatioVisualEditor } from '../group-ratio-visual-editor'", "import { GroupRatioVisualEditor, type GroupSettingsSection } from '../group-ratio-visual-editor'")
change(p, '  const [settings, setSettings] = useState<Record<string, string>>({', "  const [section, setSection] = useState<GroupSettingsSection>('pricing')\n  const [settings, setSettings] = useState<Record<string, string>>({")
change(p, '    <GroupRatioVisualEditor\n', '    <GroupRatioVisualEditor\n      section={section}\n      onSectionChange={setSection}\n      defaultUseAutoGroupField={null}\n')
change(p, 'Save group ratios', 'Save group settings', 6)
change('web/src/features/system-settings/models/__tests__/ratio-settings-stale-save.test.tsx', 'Save group ratios', 'Save group settings', 3)
p = 'web/src/features/system-settings/models/__tests__/group-settings.test.tsx'
change(p, '        onSave={props.onSave ?? (async () => {})}', '''        baseline={{ ...defaults, ...props.initial }}
        onSave={async (values) => {
          await props.onSave?.(values)
          return 'saved'
        }}''')
change('web/src/features/users/components/users-columns.tsx', '    [t, quotaUnit, currencyConfig]', '''    // formatQuota reads the currency configuration from the store.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [t, quotaUnit, currencyConfig]''')
p = 'web/src/features/usage-logs/components/__tests__/usage-facts.test.tsx'
change(p, 'function renderDetails(other: LogOtherData, promptTokens = 0): QueryClient {', 'function renderDetails(other: LogOtherData, promptTokens = 0, isAdmin = false): QueryClient {')
change(p, 'isAdmin={false}', 'isAdmin={isAdmin}')
change(p, """test('shows the recorded request and response models in log details', () => {
  const queryClient = renderDetails({
    response_model: {
      requested_model: 'requested-model',
      upstream_model: 'mapped-model',
      returned_model: 'unexpected-model',
    },
  })""", """test('shows recorded upstream model observations to administrators', () => {
  const queryClient = renderDetails({
    admin_info: {
      response_model: {
        requested_model: 'requested-model',
        upstream_model: 'mapped-model',
        returned_model: 'unexpected-model',
      },
    },
  }, 0, true)""")
change(p, "describe('usage facts billing details', () => {", """test('does not disclose legacy or admin-only upstream model observations to users', () => {
  const observation = {
    requested_model: 'requested-model',
    upstream_model: 'mapped-model',
    returned_model: 'unexpected-model',
  }
  const queryClient = renderDetails({
    response_model: observation,
    admin_info: { response_model: observation },
  })
  expect(screen.queryByText('Response model: unexpected-model')).not.toBeInTheDocument()
  expect(screen.queryByText('mapped-model')).not.toBeInTheDocument()
  expect(screen.queryByText('unexpected-model')).not.toBeInTheDocument()
  queryClient.clear()
})

describe('usage facts billing details', () => {""")

changed = subprocess.check_output(['git', 'diff', '--name-only'], text=True).splitlines()
files = [p for p in changed if p.endswith('.go')] + ['controller/plugin_protocol_image_privacy_test.go']
subprocess.run(['gofmt', '-w', *files], check=True)
subprocess.run(['node', 'scripts/add-copyright.mjs'], cwd='web', check=True)
marker.write_text(json.dumps({'base_candidate':'a1ee61c51d727f74880d5570628e65f0c9b5177a','fixes':['shared retry entry point','task-plugin image privacy boundary','split wallet reservation regression fixtures','token-scoped model cache test fixtures','group rename and stale save UI regressions','admin-only model visibility tests'],'acceptance':'pending exact candidate checks'}, indent=2)+'\n')
print('Reviewed follow-up changes applied. No tests are disabled; acceptance remains pending.')
