"""Replay reviewed rc40 conflict resolutions in an isolated Actions checkout.
Refuse different merge stages. No blanket ours/theirs fallback is permitted.
"""
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess

if os.environ.get('GITHUB_ACTIONS') != 'true' or os.environ.get('GITHUB_REF') != 'refs/heads/codex/rc40-full-integration-20260922':
    raise SystemExit('Only the isolated rc40 integration Actions branch may execute this replay.')
if os.environ.get('GITHUB_REPOSITORY') != '1057300248/wanchuan-new-api':
    raise SystemExit('Unexpected repository')
EXPECTED_STAGES_SHA256 = '81b6414f24e23db79d1460768b9b344ad69f165ef35eeda81f04b9d8ef6e96b7'
if hashlib.sha256(subprocess.check_output(['git', 'ls-files', '-u'])).hexdigest() != EXPECTED_STAGES_SHA256:
    raise SystemExit('Merge stages differ from reviewed pins; refusing speculative resolution')
ROOT = Path.cwd()
RECORDS = []
PAT = re.compile(r'^<<<<<<<[^\n]*\n(.*?)^=======\n(.*?)^>>>>>>>[^\n]*\n?', re.M | re.S)


def resolve(path, decisions, reason):
    p = ROOT / path
    original = p.read_text()
    blocks = list(PAT.finditer(original))
    assert blocks, path
    if isinstance(decisions, str) or callable(decisions):
        decisions = [decisions] * len(blocks)
    assert len(blocks) == len(decisions), (path, len(blocks), len(decisions))
    it = iter(decisions)
    def choose(m):
        d = next(it)
        a, b = m.groups()
        return d(a, b) if callable(d) else {'ours': a, 'theirs': b, 'both': a + b}[d]
    result = PAT.sub(choose, original)
    assert '<<<<<<<' not in result
    p.write_text(result)
    RECORDS.append({'path': path, 'hunks': len(blocks), 'reason': reason})


def replace(path, old, new):
    p = ROOT / path
    text = p.read_text()
    assert text.count(old) == 1, (path, text.count(old), old[:70])
    p.write_text(text.replace(old, new))


def side(path, stage, reason):
    (ROOT / path).write_bytes(subprocess.check_output(['git', 'show', f':{stage}:{path}']))
    RECORDS.append({'path': path, 'stage': stage, 'reason': reason})


resolve('.agents/rules/billing.md', 'theirs', 'Update image billing contract for rc39 task-plugin migration; retain safety invariants.')
resolve('.gitignore', 'ours', 'Retain local output and secret-directory exclusions.')
resolve('AGENTS.md', 'theirs', 'Restore explicit billing read-gate triggers without duplicating rules.')
resolve('controller/auth_flow_test.go', 'ours', 'Retain commit-time email, status, identity and auth-version race regressions.')
resolve('controller/oauth.go', 'ours', 'Retain locked identity/email-evidence revalidation before issuing a session.')
resolve('controller/relay.go', 'ours', 'Retain no-retry-after-stream-error guard.')
resolve('controller/relay_task_plugin_test.go', [lambda a,b:a+'}\n\n'+b, 'theirs', 'theirs'], 'Keep execution identity AND upstream retainResult coverage; use shared dialect fixture with wallet ledger tables.')
reg = '''	registry := moneyport.NewFactoryRegistry()
	require.NoError(t, service.RegisterHostFundingFactories(registry))
	require.NoError(t, walletmodule.RegisterFundingFactory(registry))
	if err := service.ConfigureFundingFactoryRegistry(registry); err != nil {
		require.Contains(t, err.Error(), "already configured")
	}
'''
replace('controller/relay_task_plugin_test.go', 'func TestImmediateTaskSettlementDatabase(t *testing.T) {\n\tdb, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Channel{}, &model.Task{}, &model.Log{})', 'func TestImmediateTaskSettlementDatabase(t *testing.T) {\n'+reg+'\tdb, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Channel{}, &model.Task{}, &model.Log{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{})')
wschoices = ['ours'] * 17
wschoices[6] = 'theirs'
wschoices[7] = lambda a,b:a.replace('2990', '2000')
resolve('controller/responses_websocket_test.go', wschoices, 'Retain PostgreSQL/Redis, split-ledger and privacy regressions; fixed-charge rejection fixture reflects input-only reservation.')
resolve('middleware/distributor.go', 'ours', 'Preserve all playground routes and token-specific effective group context.')
resolve('middleware/distributor_test.go', 'theirs', 'Add multiple-plugin New API routing and rebinding regressions.')
resolve('model/account_security.go', 'ours', 'Retain transactional claim uniqueness and commit-time identity evidence checks.')
resolve('model/task.go', [lambda a,b:a.replace('len(p.PluginState) == 0 && p.PollFailures == 0 {', 'len(p.PluginState) == 0 && p.PollFailures == 0 && !p.ResultDiscarded {')], 'Persist result retention together with wallet debit and legacy request identity.')
resolve('model/user.go', 'ours', 'Retain duplicate/deleted GitHub binding rejection.')
resolve('pkg/perf_metrics/metrics_test.go', 'theirs', 'Add asynchronous terminal-task metric regressions.')
resolve('relay/channel/openai/relay-openai.go', 'ours', 'Retain typed stream-failure accounting and no false success.')
resolve('relay/channel/openai/relay_responses.go', 'ours', 'Retain model masking, terminal failure emission and disconnect semantics.')
resolve('relay/common/stream_status.go', 'ours', 'Retain typed provider-declared stream errors used by settlement.')
resolve('relay/convert_request_error_test.go', 'theirs', 'Add classified Ali image conversion error regression.')
resolve('relay/image_handler.go', 'both', 'Retain JSON DTO support and classified-error handling imports.')
resolve('relay/response_model_test.go', 'ours', 'Keep upstream model observations in admin-only fields.')
resolve('relay/responses_websocket.go', 'ours', 'Retain user-visible WebSocket privacy boundary.')
resolve('relay/responses_websocket_test.go', 'ours', 'Retain success-frame precision and recognized-error sanitization regressions.')
resolve('service/log_info_generate.go', 'ours', 'Keep diagnostics and upstream model identity out of user logs.')
resolve('service/login_verification.go', 'ours', 'Use stricter custom transaction signature consistently.')
resolve('service/task_billing_test.go', 'both', 'Retain money-port fixtures and new plugin billing fixtures.')

for path in ['web/src/components/floating-window.tsx', 'web/src/components/floating-window/__tests__/floating-window.test.tsx', 'web/src/components/multi-select.tsx']:
    resolve(path, 'theirs', 'Integrate expanded floating-window and icon support; preserve nonconflicting custom behavior.')
for path in ['web/src/features/channels/components/__tests__/channel-configuration.test.tsx', 'web/src/features/channels/components/__tests__/model-mapping-batch-dialog.test.tsx', 'web/src/features/channels/components/__tests__/upstream-model-selection.test.tsx', 'web/src/features/channels/components/dialogs/upstream-update-dialog.tsx', 'web/src/features/channels/components/model-mapping-batch-dialog.tsx', 'web/src/features/channels/components/model-redirect-panel.tsx']:
    resolve(path, 'theirs', 'Use explicit select/preview/confirm mapping workflow; retire implicit guessed mapping UI and update behavioral tests.')
resolve('web/src/features/channels/components/__tests__/model-mapping-editor.test.tsx', 'ours', 'Retain fireEvent used by original mapping regression and keep upstream added tests.')
resolve('web/src/features/channels/components/drawers/channel-mutate-drawer.tsx', ['both'] + ['theirs'] * 10, 'Retain Cline channel type and integrate New API multiple task plugin binding; remove auto-guessed alias UI.')
for path in ['web/src/features/channels/lib/header-passthrough.ts', 'web/src/features/channels/lib/model-mapping-rules.ts']:
    resolve(path, 'theirs', 'Whitespace-only overlap.')
resolve('web/src/features/keys/components/dialogs/cc-switch-dialog.tsx', 'theirs', 'Normalize cleared combobox values without changing token-scoped model/group/protocol discovery.')
resolve('web/src/features/pricing/__tests__/task-matrix-display.test.ts', 'theirs', 'Remove duplicate Vitest import; retain conditional task display regressions.')
resolve('web/src/features/pricing/components/model-details-uptime-sparkline.tsx', 'theirs', 'Unify repeated status branches with equivalent labels and colors.')
resolve('web/src/features/system-info/components/system-tasks-panel.tsx', 'theirs', 'Use active-task/history layout and shared empty/error states.')
replace('web/src/features/system-info/components/system-tasks-panel.tsx', "import type { ReactNode } from 'react'\n", '')
for path in ['web/src/features/system-settings/billing/index.tsx', 'web/src/features/system-settings/billing/section-registry.tsx', 'web/src/features/system-settings/general/quota-settings-section.tsx', 'web/src/features/system-settings/types.ts']:
    resolve(path, lambda a,b:re.sub(r'^.*PreConsumedQuota:.*\n', '', a, flags=re.M), 'Retain ChuanAPI signup/affiliate settings; remove obsolete pre-consume floor; integrate new quota fields without resurrecting legacy invite bonuses.')
resolve('web/src/features/system-settings/models/__tests__/group-pricing-decimals.test.tsx', 'theirs', 'Provide required tab props to updated pricing editor regression.')
resolve('web/src/features/system-settings/models/group-ratio-form.tsx', [
    lambda a,b:a.replace("import { safeJsonParse } from '../utils/json-parser'\n", '').replace("import { GroupRatioVisualEditor } from './group-ratio-visual-editor'", "import { GroupRatioVisualEditor, type GroupSettingsSection } from './group-ratio-visual-editor'").replace("import { GroupSpecialUsableRulesEditor } from './group-special-usable-editor'\n", ''),
    'both', 'theirs',
    lambda a,b:b.replace('form.handleSubmit(onSave,', 'form.handleSubmit(handleSave,').replace('disabled={isSaving}', 'disabled={submitting}').replace("{isSaving ? t('Saving...')", "{submitting ? t('Saving...')"),
    lambda a,b:b.replace('<GroupRatioVisualEditor\n', '<GroupRatioVisualEditor\n            key={renameSaveVersion}\n            pendingRenames={groupRenames}\n            onRenamesChange={handleRenamesChange}\n'),
], 'Preserve transactional group rename planning/submission while adopting tabs, validation focus and reorder UI.')
replace('web/src/features/system-settings/models/group-ratio-form.tsx', "import { memo, useCallback, useState, type ReactNode } from 'react'", "import { memo, useCallback, useMemo, useState, type ReactNode } from 'react'")
resolve('web/src/features/system-settings/models/group-ratio-visual-editor.tsx', [
    'theirs', 'both',
    lambda a,b:b.replace('topupGroupRatio={topupGroupRatio}\n', 'topupGroupRatio={topupGroupRatio}\n          pendingRenames={pendingRenames}\n          onRenamesChange={onRenamesChange}\n'),
], 'Retain pending rename resolution in pricing table with shared drag/keyboard reorder component and settings tabs.')
for path in ['__tests__/routing.test.tsx', 'api.ts', 'decision-record.tsx', 'policy-label.ts', 'related-policy-link.tsx', 'routing-form.ts', 'routing-section.tsx', 'use-save-policy.ts']:
    resolve('web/src/features/system-settings/request-policies/' + path, 'ours', 'Retain existing copyright header; behavioral changes merge outside hunk.')
for path in ['billing-source-visibility.test.tsx', 'cost-display.test.tsx']:
    resolve('web/src/features/usage-logs/components/__tests__/' + path, 'theirs', 'Keep historical subscription billing visible after expiration; wallet marker remains conditional.')
resolve('web/src/features/usage-logs/components/__tests__/mobile-card.test.tsx', 'ours', 'Retain auth-store cleanup isolation.')
resolve('web/src/features/usage-logs/components/__tests__/model-badge.test.tsx', 'theirs', 'Use waitFor for upstream added asynchronous tests.')
side('web/src/features/usage-logs/components/columns/common-logs-columns.tsx', 3, 'Whitespace-insensitive diff reviewed: integrate memo/currency dependency/task_sync; reapply admin-only model observation difference.')
replace('web/src/features/usage-logs/components/columns/common-logs-columns.tsx', 'formatModelName(log)', 'formatModelName(log, isAdmin)')
resolve('web/src/features/usage-logs/components/dialogs/details-dialog.tsx', 'ours', 'Keep admin-only model observations and mapping visibility.')
resolve('web/src/features/usage-logs/components/log-cost-display.tsx', 'theirs', 'Show historical subscription funding independently of current subscription availability.')
resolve('web/src/features/usage-logs/lib/format.ts', 'ours', 'Restrict upstream model identity to admins.')
resolve('web/src/features/usage-logs/types.ts', 'ours', 'Retain private admin diagnostics types.')
side('web/src/features/users/components/users-columns.tsx', 2, 'Retain ChuanAPI extension flag around legacy affiliate balance; apply upstream memo optimization explicitly.')
replace('web/src/features/users/components/users-columns.tsx', "import { useTranslation } from 'react-i18next'", "import { useMemo } from 'react'\nimport { useTranslation } from 'react-i18next'")
replace('web/src/features/users/components/users-columns.tsx', "  useSystemConfigStore((state) => state.config.currency)", "  const currencyConfig = useSystemConfigStore((state) => state.config.currency)")
replace('web/src/features/users/components/users-columns.tsx', '  return [\n', '  return useMemo<ColumnDef<User>[]>(() => [\n')
replace('web/src/features/users/components/users-columns.tsx', '  ]\n}\n', '  ], [t, quotaUnit, currencyConfig])\n}\n')
path = 'web/src/features/wallet/components/dialogs/billing-history-dialog.tsx'
(ROOT / path).unlink()
RECORDS.append({'path': path, 'delete': True, 'reason': 'Do not resurrect legacy wallet dialog deliberately removed in main; current ChuanAPI account-history owns the UI and contract tests reject legacy imports.'})
resolve('web/vitest.config.ts', 'ours', 'Keep all script tests, not just oxlint; do not skip baseline/tooling regressions.')

missing = object()
for lang in ['en', 'zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi']:
    path = 'web/src/i18n/locales/' + lang + '.json'
    base, ours, theirs = [json.loads(subprocess.check_output(['git', 'show', f':{i}:{path}'])) for i in (1, 2, 3)]
    assert set(base) == set(ours) == set(theirs) == {'translation'}
    b, o, t = [v['translation'] for v in (base, ours, theirs)]
    merged = {}
    for key in sorted(b.keys() | o.keys() | t.keys()):
        bv, ov, tv = b.get(key, missing), o.get(key, missing), t.get(key, missing)
        if ov == tv:
            value = ov
        elif ov == bv:
            value = tv
        elif tv == bv:
            value = ov
        else:
            raise RuntimeError(f'Unreviewed translation conflict: {lang} {key}')
        if value is not missing:
            merged[key] = value
    (ROOT / path).write_text(json.dumps({'translation': merged}, ensure_ascii=False, indent=2) + '\n')
    RECORDS.append({'path': path, 'strategy': 'Three-way per translation key; reject conflicting values', 'keys': len(merged)})
resolve('web/src/i18n/static-keys.ts', 'both', 'Keep password policy translation and add task expression metadata keys.')
side('web/public/logo.png', 2, 'Preserve ChuanAPI brand asset, not upstream default logo.')
assert len(RECORDS) == 78, len(RECORDS)
subprocess.run(['git', 'add', '--all'], check=True)
changed = subprocess.check_output(['git', 'diff', '--cached', '--name-only', '--diff-filter=ACMR', '-z'], text=True).split('\0')
go_files = [p for p in changed if p.endswith('.go')]
if go_files:
    subprocess.run(['gofmt', '-w', *go_files], check=True)
for record in RECORDS:
    p = Path(record['path'])
    record['resolved_sha256'] = hashlib.sha256(p.read_bytes()).hexdigest() if p.is_file() else None
Path('.github/rc40/resolutions.json').write_text(json.dumps(RECORDS, indent=2) + '\n')
subprocess.run(['git', 'add', '--all'], check=True)
if subprocess.check_output(['git', 'ls-files', '-u'], text=True):
    raise SystemExit('Unresolved index entries remain')
print('Resolved all 78 reviewed conflict paths; build/tests remain mandatory.')
