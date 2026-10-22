#!/usr/bin/env bash
# Destructive integration fixtures are restricted to disposable CI service databases.
set -euo pipefail
: "${OUT:?OUT must name an evidence directory}"
: "${SOURCE_SHA:?SOURCE_SHA must identify the checkout}"
test "$(git rev-parse HEAD)" = "$SOURCE_SHA"
mkdir -p "$OUT" web/dist
touch web/dist/index.html
printf '%s\n' "$SOURCE_SHA" > "$OUT/revision.txt"
go version > "$OUT/go-version.txt"
status=0
# Never inherit an operator database or pressure opt-in in unrelated reports.
export TEST_REFUND_OPS_MYSQL_DSN= TEST_REFUND_OPS_POSTGRES_DSN= TEST_REFUND_QUEUE_PRESSURE=
export TEST_FINALIZATION_CACHE_MYSQL_DSN= TEST_FINALIZATION_CACHE_POSTGRES_DSN=
export TEST_SETTLEMENT_DEADLINE_REDIS_ADDR= TEST_SETTLEMENT_DEADLINE_REDIS_DISPOSABLE=
export TEST_TASK_REDIS_ADDR=

report() {
  local label=$1 package=$2 pattern=$3
  shift 3
  # Retain compiler stderr separately so it cannot masquerade as Go test JSON.
  # Continue collecting independent suites, but any failed suite fails the job.
  if ! go test -race -p 2 "$package" -run "$pattern" -count=1 -timeout=12m -json > "$OUT/$label.jsonl" 2> "$OUT/$label.stderr.log"; then
    cat "$OUT/$label.stderr.log" >&2
    cat "$OUT/$label.jsonl"
    status=1
  fi
  if ! python3 ops/ci/check_go_test_report.py --reject-skipped-descendants "$OUT/$label.jsonl" "$@"; then
    status=1
  fi
}

case "${1:-}" in
database)
  : "${DIALECT:?DIALECT is required}"
  export TEST_DATABASE_COMPAT=1 TEST_DATABASE_DIALECT="$DIALECT"
  export TEST_DATABASE_DSN= TEST_LOG_DATABASE_DSN=
  export TEST_MYSQL_DSN= TEST_MYSQL_LOG_DSN= TEST_POSTGRES_DSN= TEST_POSTGRES_LOG_DSN=
  export AUDIT_MYSQL_DSN= AUDIT_POSTGRES_DSN=
  token_migration_mysql_dsn= token_migration_postgres_dsn=
  # Retained model/service matrices still refuse nonempty fixture databases.
  # Never share those fixtures or inherit an ambient database DSN.
  export TEST_FINALIZATION_MODEL_MYSQL_DSN= TEST_FINALIZATION_MODEL_POSTGRES_DSN=
  export TEST_FINALIZATION_SERVICE_MYSQL_DSN= TEST_FINALIZATION_SERVICE_POSTGRES_DSN=
  finalization_model_mysql_dsn= finalization_model_postgres_dsn=
  finalization_service_mysql_dsn= finalization_service_postgres_dsn=
  case "$DIALECT" in
  mysql)
    : "${MYSQL_ID:?a real MySQL service is required}" "${MYSQL_PORT:?a real MySQL port is required}"
    docker inspect --format '{{.Config.Image}} {{.Image}}' "$MYSQL_ID" > "$OUT/database-image.txt"
    docker exec "$MYSQL_ID" mysql -uroot -pcompat -e 'SELECT VERSION(); ALTER DATABASE repair_compat CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci; CREATE DATABASE repair_compat_log CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci; CREATE DATABASE repair_token_migration CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;' > "$OUT/database-version.txt"
    docker exec "$MYSQL_ID" mysql -uroot -pcompat -e 'CREATE DATABASE repair_finalization_model CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci; CREATE DATABASE repair_finalization_service CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;'
    finalization_model_mysql_dsn="root:compat@tcp(127.0.0.1:$MYSQL_PORT)/repair_finalization_model?charset=utf8mb4&parseTime=true&loc=UTC"
    finalization_service_mysql_dsn="root:compat@tcp(127.0.0.1:$MYSQL_PORT)/repair_finalization_service?charset=utf8mb4&parseTime=true&loc=UTC"
    export TEST_MYSQL_DSN="root:compat@tcp(127.0.0.1:$MYSQL_PORT)/repair_compat?charset=utf8mb4&parseTime=true&loc=UTC"
    export TEST_MYSQL_LOG_DSN="root:compat@tcp(127.0.0.1:$MYSQL_PORT)/repair_compat_log?charset=utf8mb4&parseTime=true&loc=UTC"
    export TEST_DATABASE_DSN="$TEST_MYSQL_DSN" TEST_LOG_DATABASE_DSN="$TEST_MYSQL_LOG_DSN"
    token_migration_mysql_dsn="root:compat@tcp(127.0.0.1:$MYSQL_PORT)/repair_token_migration?charset=utf8mb4&parseTime=true&loc=UTC"
    ;;
  postgres)
    : "${PG_ID:?a real PostgreSQL service is required}" "${PG_PORT:?a real PostgreSQL port is required}"
    docker inspect --format '{{.Config.Image}} {{.Image}}' "$PG_ID" > "$OUT/database-image.txt"
    docker exec "$PG_ID" psql -U postgres -d repair_compat -Atc 'SHOW server_version;' > "$OUT/database-version.txt"
    docker exec "$PG_ID" createdb -U postgres repair_compat_log
    docker exec "$PG_ID" createdb -U postgres repair_token_migration
    docker exec "$PG_ID" createdb -U postgres repair_finalization_model
    docker exec "$PG_ID" createdb -U postgres repair_finalization_service
    finalization_model_postgres_dsn="postgres://postgres:compat@127.0.0.1:$PG_PORT/repair_finalization_model?sslmode=disable"
    finalization_service_postgres_dsn="postgres://postgres:compat@127.0.0.1:$PG_PORT/repair_finalization_service?sslmode=disable"
    export TEST_POSTGRES_DSN="postgres://postgres:compat@127.0.0.1:$PG_PORT/repair_compat?sslmode=disable"
    export TEST_POSTGRES_LOG_DSN="postgres://postgres:compat@127.0.0.1:$PG_PORT/repair_compat_log?sslmode=disable"
    export TEST_DATABASE_DSN="$TEST_POSTGRES_DSN" TEST_LOG_DATABASE_DSN="$TEST_POSTGRES_LOG_DSN"
    token_migration_postgres_dsn="postgres://postgres:compat@127.0.0.1:$PG_PORT/repair_token_migration?sslmode=disable"
    ;;
  sqlite) ;;
  *) echo "Unsupported database dialect: $DIALECT" >&2; exit 1 ;;
  esac
  export AUDIT_MYSQL_DSN="$TEST_MYSQL_DSN" AUDIT_POSTGRES_DSN="$TEST_POSTGRES_DSN"

  # Production startup installs an immutable registry; keep it in its own process.
  report startup ./model '^TestDatabaseCompatibilityReal$' TestDatabaseCompatibilityReal
  # New admin/config ports must not hide unconfigured database skips.
  prefix="TestChannelPriorityDatabaseMatrix/$DIALECT"
  report channel-priority ./model "^TestChannelPriorityDatabaseMatrix$/$DIALECT$" "$prefix"
  prefix="TestAuditLogSortIndexDatabaseMatrix/$DIALECT"
  report audit-ordered-index ./model "^TestAuditLogSortIndexDatabaseMatrix$/$DIALECT$" "$prefix/upgrade=false" "$prefix/upgrade=true"
  prefix="TestRootAffCodeUniqueIndexDatabaseMatrix/$DIALECT"
  report setup-aff-code-index ./model "^TestRootAffCodeUniqueIndexDatabaseMatrix$/$DIALECT$" "$prefix/fresh" "$prefix/repeated_startup"
  case "$DIALECT" in
    sqlite) migration=TestTokenMigrationFromChar48ToVarchar128 ;;
    mysql) migration=TestTokenMigrationFromChar48ToVarchar128MySQL ;;
    postgres) migration=TestTokenMigrationFromChar48ToVarchar128Postgres ;;
  esac
  # This safety-sensitive legacy-schema test refuses an existing tokens table.
  # Use a fresh database owned by this disposable service, never the startup DB;
  # service teardown removes it without dropping any externally supplied data.
  TEST_MYSQL_DSN="$token_migration_mysql_dsn" TEST_POSTGRES_DSN="$token_migration_postgres_dsn" \
    report token-migration ./controller "^$migration$" "$migration"
  prefix="TestAPITokenAuditDatabaseMatrix/$DIALECT"
  report token-audit ./controller "^TestAPITokenAuditDatabaseMatrix$/$DIALECT$" "$prefix/separate_log=false" "$prefix/separate_log=true"
  export TEST_PERF_MYSQL_DSN="$TEST_MYSQL_DSN" TEST_PERF_POSTGRES_DSN="$TEST_POSTGRES_DSN"
  prefix="TestPerformanceAggregationAndFlush/$DIALECT"
  report performance-aggregation ./pkg/perf_metrics "^TestPerformanceAggregationAndFlush$/$DIALECT$" "$prefix" "$prefix/recent_models_are_summarized_over_their_status_sample_hours" "$prefix/recent_history_stops_at_the_7_day_boundary_even_with_fewer_than_24_samples"
  prefix="TestRuntimeFinalFlushRetainsFailedSamplesAndPersistsCurrentBucket/$DIALECT"
  report performance-runtime-flush ./pkg/perf_metrics "^TestRuntimeFinalFlushRetainsFailedSamplesAndPersistsCurrentBucket$/$DIALECT$" "$prefix"
  prefix="TestSetupWalletDatabaseMatrix/$DIALECT"
  required=("$prefix/fresh_root_and_replay" "$prefix/concurrent_fresh_setup_single_credit" "$prefix/rollback_balance_ledger_operations" "$prefix/rollback_balance_ledger" "$prefix/setup_record_failure_retry" "$prefix/paid_only_preconsume" "$prefix/invalid_credentials_no_account_or_grant")
  for account in empty legacy bonus_only split; do
    required+=("$prefix/existing_root_$account")
  done
  report setup-wallet ./controller "^TestSetupWalletDatabaseMatrix$/$DIALECT$" "${required[@]}"
  prefix="TestTaskSettlementMigrationDatabaseMatrix/$DIALECT"
  report task-migration ./model "^TestTaskSettlementMigrationDatabaseMatrix$/$DIALECT$" "$prefix/fresh" "$prefix/upgrade"
  prefix="TestQuotaDataCacheUsageDatabaseMatrix/$DIALECT"
  report dashboard-migration ./model "^TestQuotaDataCacheUsageDatabaseMatrix$/$DIALECT$" "$prefix/fresh" "$prefix/upgrade"
  prefix="TestMidjourneyWalletReceiptMigrationDatabaseMatrix/$DIALECT"
  report midjourney-migration ./model "^TestMidjourneyWalletReceiptMigrationDatabaseMatrix$/$DIALECT$" "$prefix/upgrade=false" "$prefix/upgrade=true"
  prefix="TestTaskWalletReceiptsDatabaseMatrix/$DIALECT"
  required=("$prefix")
  for contract in \
    'legacy_paid_charge_is_included_in_first_receipt' \
    'midjourney_charge_rolls_back_on_midjourneys_failure' \
    'midjourney_charge_rolls_back_on_tokens_failure' \
    'midjourney_failure_before_charge_never_debits' \
    'midjourney_restores_original_accounts_from_stale_observer' \
    'official_midjourney_refund_independent_midjourneys_failure' \
    'official_midjourney_refund_independent_tokens_failure' \
    'official_refund_independent_tasks_failure' \
    'official_refund_independent_tokens_failure' \
    'official_subscription_settlement_survives_token_failure' \
    'partial_refund_persists_remaining_accounts' \
    'settlement_falls_back_and_persists_cumulative_receipt' \
    'settlement_keeps_existing_bonus_exclusion' \
    'settlement_rolls_back_on_tasks_failure' \
    'settlement_rolls_back_on_tokens_failure'; do
    required+=("$prefix/$contract")
  done
  report wallet ./service "^TestTaskWalletReceiptsDatabaseMatrix$/$DIALECT$" "${required[@]}"
  # Official nested subscription behavior uses the existing unique owned table/schema helper.
  # SQLite's exact 517 result proves parity/partial-credit risk, not financial safety.
  prefix="TestOfficialSubscriptionRefundDatabaseBehavior/$DIALECT"
  report official-subscription-refund ./model "^TestOfficialSubscriptionRefundDatabaseBehavior$/$DIALECT$" TestOfficialSubscriptionRefundDatabaseBehavior "$prefix"
  prefix="TestSubscriptionPeriodMigrationDatabaseMatrix/$DIALECT"
  report subscription-period-migration ./model "^TestSubscriptionPeriodMigrationDatabaseMatrix$/$DIALECT$" "$prefix/fresh" "$prefix/upgrade"
  report owner ./model "^TestPublicModelOwnerDatabaseMatrix$/$DIALECT$" "TestPublicModelOwnerDatabaseMatrix/$DIALECT"
  report owner-group-settings ./model "^TestRequestPolicyDatabaseMatrix$/$DIALECT$" "TestRequestPolicyDatabaseMatrix/$DIALECT"
  prefix="TestMonitorHiddenGroupsDatabaseMatrix/$DIALECT"
  required=("$prefix/production_json_reload" "$prefix/fresh_default_insert" "$prefix/corrupt_saved_visibility_fails_closed" "$prefix/failed_write_preserves_saved_visibility" "$prefix/concurrent_legacy_write_preserves_visibility" "$prefix/connection_reopen_preserves_visibility")
  for contract in omitted_preserves null_preserves empty_clears false_clears explicit_replaces invalid_key_rejected invalid_false_key_rejected invalid_type_rejected too_many_rejected; do
    required+=("$prefix/save_contract/$contract")
  done
  report monitor-visibility ./modules/chuanapi/console/monitor "^TestMonitorHiddenGroupsDatabaseMatrix$/$DIALECT$" "${required[@]}"
  prefix="TestSensitiveExemptionGroupRenameDatabaseMatrix/$DIALECT"
  required=("$prefix/rename_preserves_policy_not_old_name" "$prefix/collision_and_coordinated_removal" "$prefix/explicit_options_normalize_and_revoke" "$prefix/invalid_or_stale_explicit_options_rollback" "$prefix/stale_policy_writes_and_no_group_definition" "$prefix/explicit_reintroduction_remains_possible" "$prefix/policy_save_precedes_rename" "$prefix/concurrent_rename_policy_save/rollback=false" "$prefix/concurrent_rename_policy_save/rollback=true")
  report sensitive-group-rename ./model "^TestSensitiveExemptionGroupRenameDatabaseMatrix$/$DIALECT$" "${required[@]}"
  export TEST_FIXED_MYSQL_DSN="$TEST_MYSQL_DSN" TEST_FIXED_MYSQL_LOG_DSN="$TEST_MYSQL_LOG_DSN"
  export TEST_FIXED_POSTGRES_DSN="$TEST_POSTGRES_DSN" TEST_FIXED_POSTGRES_LOG_DSN="$TEST_POSTGRES_LOG_DSN"
  prefix="TestConvertedResponsesFailureAccountingDatabaseMatrix/$DIALECT"
  required=("$prefix")
  for format in openai claude gemini; do
    for outcome in failed bare_error cancelled failed_status eof done_without_terminal incomplete success empty_failure empty_eof fixed_partial fixed_empty fixed_success_missing_usage; do
      required+=("$prefix/buffered/$format/$outcome")
    done
  done
  report converted-responses ./relay "^TestConvertedResponsesFailureAccountingDatabaseMatrix$/$DIALECT/" "${required[@]}"
  prefix="TestNativeResponsesFailureFixedAccountingDatabaseMatrix/$DIALECT"
  report native-responses ./relay "^TestNativeResponsesFailureFixedAccountingDatabaseMatrix$/$DIALECT/" "$prefix" "$prefix/http_nonstream/failed/empty" "$prefix/http_nonstream/failed/partial_usage" "$prefix/http_request/failed_partial_usage" "$prefix/zero_settlement_transaction_failure_recovers_reserve"
  export TEST_TASK_DB_DIALECT="$DIALECT"
  prefix=TestImmediateTaskSettlementDatabase
  required=("$prefix")
  for contract in partial zero larger invalid_usage expression_failure negative_result missing_usage failed official_settlement_failure_after_insert official_upward_shortage_before_insert; do
    required+=("$prefix/$contract")
  done
  report immediate-settlement ./controller '^TestImmediateTaskSettlementDatabase$' "${required[@]}"


  # Required current-policy leaves are mirrored by offline orchestration tests.
  prefix="TestBillingFinalizationDatabaseMatrix/$DIALECT"
  required=("$prefix")
  for contract in \
    'migration_preserves_pending_and_tombstones' \
    'near_miss_realtime_key_rejected/wss:+1:20' \
    'near_miss_realtime_key_rejected/wss:01:20' \
    'near_miss_realtime_key_rejected/wss:0:20' \
    'near_miss_realtime_key_rejected/wss:18446744073709551616:20' \
    'near_miss_realtime_key_rejected/wss:1:21' \
    'receipt_mismatch_and_legacy_replay' \
    'rollback_probe_no_permanent_owner'; do
    required+=("$prefix/$contract")
  done
  TEST_FINALIZATION_MODEL_MYSQL_DSN="$finalization_model_mysql_dsn" TEST_FINALIZATION_MODEL_POSTGRES_DSN="$finalization_model_postgres_dsn" \
    report request-finalization-model ./model "^TestBillingFinalizationDatabaseMatrix$/$DIALECT$" "${required[@]}"

  prefix="TestBillingFinalizationSessionDatabaseMatrix/$DIALECT"
  required=("$prefix")
  for contract in \
    'commit_boundaries/lost_ack/settlement' \
    'commit_boundaries/rollback/settlement' \
    'extension_capability/custom_billing_settler_insert' \
    'extension_capability/custom_non_wallet' \
    'extension_capability/custom_subscription_delta' \
    'extension_capability/custom_wallet_atomic_replay' \
    'failed_settle_reserve_refund' \
    'independent_violation_fee/fee_before_refund' \
    'independent_violation_fee/refund_before_fee' \
    'legacy_final_more_less/a' \
    'legacy_final_more_less/Å' \
    'legacy_final_more_less/ĕ' \
    'probe_failure' \
    'real_session_partial_less' \
    'real_session_partial_more' \
    'real_session_playground' \
    'real_session_subscription_zero_delta' \
    'real_session_subscription_zero_delta_replaced_token' \
    'real_session_trusted' \
    'real_session_unlimited' \
    'realtime_increment_terminal/failed_final_then_refund' \
    'realtime_increment_terminal/final_settle' \
    'realtime_increment_terminal/refund_initial_hold' \
    'separate_held_amounts' \
    'zero_delta_replacement/hold_0/key' \
    'zero_delta_replacement/hold_0/owner' \
    'zero_delta_replacement/hold_100/key' \
    'zero_delta_replacement/hold_100/owner' \
    'settlement_deadline_driver_pool/driver' \
    'settlement_deadline_driver_pool/connection' \
    'settlement_deadline_commit_evidence/wallet/rolled_back' \
    'settlement_deadline_commit_evidence/wallet/committed_lost_ack' \
    'settlement_deadline_redis/precommit_fence' \
    'settlement_deadline_redis/postcommit_cache' \
    'settlement_deadline_redis/zero_delta_cache' \
    'settlement_deadline_redis/terminal_replay_cache' \
    'settlement_deadline_redis/live_redis_error' \
    'settlement_cancelled_client' \
    'official_request_source_failure' \
    'official_request_token_failure' \
    'official_subscription_three_attempts' \
    'official_task_independent_steps/source' \
    'official_task_independent_steps/token' \
    'official_task_independent_steps/quota_clear' \
    'official_post_commit_errors/request_token' \
    'official_post_commit_errors/task_quota_clear' \
    'historical_pending_read_only/flag' \
    'historical_pending_read_only/private_only' \
    'historical_pending_read_only/both' \
    'inherited_093_allocation_limits/small_three_account_remainder' \
    'inherited_093_allocation_limits/submit_before_task_refund' \
    'inherited_093_allocation_limits/task_refund_before_submit' \
    'official_refund_before_lost_ack_readback'; do
    required+=("$prefix/$contract")
  done
  if [ "$DIALECT" != sqlite ]; then
    required+=("$prefix/settlement_deadline_row_lock")
  fi
  TEST_FINALIZATION_SERVICE_MYSQL_DSN="$finalization_service_mysql_dsn" TEST_FINALIZATION_SERVICE_POSTGRES_DSN="$finalization_service_postgres_dsn" \
    report request-finalization-service ./service "^TestBillingFinalizationSessionDatabaseMatrix$/$DIALECT$" "${required[@]}"

  # Canceled refund dispatcher/cache/period/pending-recovery reports and their
  # databases are retired in billing_policy_replacement.json, never relabeled.
  if [ "$DIALECT" = sqlite ]; then
    # Retained sanitized error codes are not a refund retry/worker contract.
    required=(
      'TestBillingErrorClassificationExcludesSecretDetails'
      'TestBillingErrorClassificationExcludesSecretDetails/nil'
      'TestBillingErrorClassificationExcludesSecretDetails/unknown_secret'
      'TestBillingErrorClassificationExcludesSecretDetails/wrapped_unknown_secret'
      'TestBillingErrorClassificationExcludesSecretDetails/timeout'
      'TestBillingErrorClassificationExcludesSecretDetails/cancel'
      'TestBillingErrorClassificationExcludesSecretDetails/identity'
      'TestBillingErrorClassificationExcludesSecretDetails/missing'
      'TestBillingErrorClassificationExcludesSecretDetails/terminal_owner'
      'TestBillingErrorClassificationExcludesSecretDetails/ledger_conflict'
      'TestBillingErrorClassificationExcludesSecretDetails/ledger_invalid'
    )
    report billing-error-classifier ./model '^TestBillingErrorClassificationExcludesSecretDetails$' "${required[@]}"
    required=(
      'TestOfficialTokenRefundUsesBatchAndIndependentSQL'
      'TestOfficialTokenRefundUsesAsyncCacheWithoutPrivateFence'
      'TestBillingTokenContextPreservesFence'
      'TestBillingTokenRedisErrorRetainsLegacyLiveContextPolicy'
      'TestBillingTokenExpiredDeadlineBeforeNotificationRollsBackSource'
    )
    report official-token-refund ./model '^(TestOfficialTokenRefundUsesBatchAndIndependentSQL|TestOfficialTokenRefundUsesAsyncCacheWithoutPrivateFence|TestBillingTokenContextPreservesFence|TestBillingTokenRedisErrorRetainsLegacyLiveContextPolicy|TestBillingTokenExpiredDeadlineBeforeNotificationRollsBackSource)$' "${required[@]}"
    required=(
      'TestOfficialMidjourneyFailedQuotaDoesNotSchedulePrivateRetry'
      'TestMidjourneyTransientChannelLookupCannotAuthorizeRefund'
      'TestMidjourneyEmptyUpstreamIDsHaveIndependentRefundReceipts'
      'TestMidjourneyRefundPreservesLegacyAndUnchargedReceipts'
      'TestMidjourneyRefundPreservesLegacyAndUnchargedReceipts/legacy_null'
      'TestMidjourneyRefundPreservesLegacyAndUnchargedReceipts/uncharged_empty'
      'TestMidjourneyCacheMissRequiresAuthoritativeAbsence'
    )
    report official-midjourney-refund ./controller '^(TestOfficialMidjourneyFailedQuotaDoesNotSchedulePrivateRetry|TestMidjourneyTransientChannelLookupCannotAuthorizeRefund|TestMidjourneyEmptyUpstreamIDsHaveIndependentRefundReceipts|TestMidjourneyRefundPreservesLegacyAndUnchargedReceipts|TestMidjourneyCacheMissRequiresAuthoritativeAbsence)$' "${required[@]}"
    required=(
      'TestOfficialCompatibilityKeeps093RefundRemainder'
    )
    report official-wallet-allocation ./modules/chuanapi/wallet '^(TestOfficialCompatibilityKeeps093RefundRemainder)$' "${required[@]}"
    required=(
      'TestWalletSettlementSurvivesTokenDeletion'
      'TestWalletSettlementSurvivesTokenDeletion/soft/trusted'
      'TestWalletSettlementSurvivesTokenDeletion/soft/charge'
      'TestWalletSettlementSurvivesTokenDeletion/soft/refund'
      'TestWalletSettlementSurvivesTokenDeletion/hard/trusted'
      'TestWalletSettlementSurvivesTokenDeletion/hard/charge'
      'TestWalletSettlementSurvivesTokenDeletion/hard/refund'
      'TestWalletSettlementRejectsChangedTokenIdentity'
      'TestWalletSettlementRejectsChangedTokenIdentity/owner'
      'TestWalletSettlementRejectsChangedTokenIdentity/credential'
      'TestPostConsumeQuotaIdempotencyCoversWalletAndToken'
      'TestPostConsumeQuotaRefundIdempotencyCoversWalletAndToken'
      'TestPostConsumeQuotaRollsBackWalletWhenTokenUpdateFails'
      'TestBillingSessionWalletSettleRollsBackFundingWhenTokenUpdateFails'
      'TestBillingSessionWalletRefundSettleRollsBackFundingWhenTokenUpdateFails'
      'TestWalletSettleFailsClosedWithoutTransactionalSettlementCapability'
      'TestWalletReserveLedgerSequenceUnchanged'
      'TestWalletReceiptOrderValuesAndDefensiveCopy'
      'TestReceiptNilForFundingWithoutReceiptCapability'
      'TestSubscriptionReserveUsesTargetDeltaWithoutDoubleCount'
      'TestReserveFundingFailureRollsBackTokenAndRetryDebitsFresh'
      'TestNeedsRefundUsesOutstandingReserveCapability'
      'TestReserveFailsClosedWithoutReservableCapability'
      'TestStreamFailurePartialUsageSettlesOnceAndNeverRefunds'
      'TestStreamFailureWithoutUsageRefundsReservationOnce'
    )
    report normal-wallet-settlement ./service '^(TestWalletSettlementSurvivesTokenDeletion|TestWalletSettlementRejectsChangedTokenIdentity|TestPostConsumeQuotaIdempotencyCoversWalletAndToken|TestPostConsumeQuotaRefundIdempotencyCoversWalletAndToken|TestPostConsumeQuotaRollsBackWalletWhenTokenUpdateFails|TestBillingSessionWalletSettleRollsBackFundingWhenTokenUpdateFails|TestBillingSessionWalletRefundSettleRollsBackFundingWhenTokenUpdateFails|TestWalletSettleFailsClosedWithoutTransactionalSettlementCapability|TestWalletReserveLedgerSequenceUnchanged|TestWalletReceiptOrderValuesAndDefensiveCopy|TestReceiptNilForFundingWithoutReceiptCapability|TestSubscriptionReserveUsesTargetDeltaWithoutDoubleCount|TestReserveFundingFailureRollsBackTokenAndRetryDebitsFresh|TestNeedsRefundUsesOutstandingReserveCapability|TestReserveFailsClosedWithoutReservableCapability|TestStreamFailurePartialUsageSettlesOnceAndNeverRefunds|TestStreamFailureWithoutUsageRefundsReservationOnce)$' "${required[@]}"
    required=(
      'TestPresentTaskSubmissionUsesNativePresenterAfterPersistence'
      'TestPresentTaskSubmissionFallbackUsesPersistedPublicID'
      'TestPresentTaskSubmissionUsesHostOpenAIVideoCreateReceipt'
      'TestExecuteTaskSubmissionRefundsWhenInsertFails'
      'TestExecuteTaskSubmissionSettlementFailureStaysDurableAndWritesNothing'
      'TestExecuteTaskSubmissionPersistsPinnedPluginProvenance'
      'TestExecuteTaskSubmissionPersistsExecutionOnlyRequestID'
      'TestExecuteTaskSubmissionHonorsRouteRetainResult'
      'TestExecuteTaskSubmissionHonorsRouteRetainResult/immediate_success_is_discarded'
      'TestExecuteTaskSubmissionHonorsRouteRetainResult/immediate_failure_is_discarded'
      'TestExecuteTaskSubmissionHonorsRouteRetainResult/asynchronous_result_is_retained'
      'TestExecuteTaskSubmissionRefundsCancellationBeforeDurableBarrier'
      'TestExecuteTaskSubmissionDisconnectBeforeUpstreamAcceptanceSkipsSubmitAndRefunds'
      'TestExecuteTaskSubmissionCallerCancellationDuringSubmitRefundsBeforeDurableBarrier'
      'TestExecuteTaskSubmissionDisconnectAfterDurableInsertDoesNotRefund'
      'TestAcceptedSubmitStreamNeverRetries'
      'TestRespondTaskSubmissionErrorWithoutCause'
      'TestExecuteTaskSubmissionRefundsWhenFinalReserveFails'
    )
    report official-task-submission ./controller '^(TestPresentTaskSubmissionUsesNativePresenterAfterPersistence|TestPresentTaskSubmissionFallbackUsesPersistedPublicID|TestPresentTaskSubmissionUsesHostOpenAIVideoCreateReceipt|TestExecuteTaskSubmissionRefundsWhenInsertFails|TestExecuteTaskSubmissionSettlementFailureStaysDurableAndWritesNothing|TestExecuteTaskSubmissionPersistsPinnedPluginProvenance|TestExecuteTaskSubmissionPersistsExecutionOnlyRequestID|TestExecuteTaskSubmissionHonorsRouteRetainResult|TestExecuteTaskSubmissionRefundsCancellationBeforeDurableBarrier|TestExecuteTaskSubmissionDisconnectBeforeUpstreamAcceptanceSkipsSubmitAndRefunds|TestExecuteTaskSubmissionCallerCancellationDuringSubmitRefundsBeforeDurableBarrier|TestExecuteTaskSubmissionDisconnectAfterDurableInsertDoesNotRefund|TestAcceptedSubmitStreamNeverRetries|TestRespondTaskSubmissionErrorWithoutCause|TestExecuteTaskSubmissionRefundsWhenFinalReserveFails)$' "${required[@]}"
    report reminder-option-validation ./model '^TestQuotaReminderThresholdOptionsRejectInvalidWithoutMutation$' TestQuotaReminderThresholdOptionsRejectInvalidWithoutMutation
    report reminder-request-validation ./controller '^(TestQuotaReminderProfileThresholdValidationPreservesLegacyFractions|TestQuotaReminderSiteOptionRejectsMalformedWireWithoutWrites)$' TestQuotaReminderProfileThresholdValidationPreservesLegacyFractions TestQuotaReminderSiteOptionRejectsMalformedWireWithoutWrites
    report frontend-embedded-assets ./common '^TestEmbeddedFrontendOnlyExposesRegularAssets$' TestEmbeddedFrontendOnlyExposesRegularAssets
    report frontend-static-cache ./middleware '^(TestFrontendAssetCacheSecurityContract|TestCacheControlByPath|TestServeFrontendFilesCompressesEachFileOnce)$' TestFrontendAssetCacheSecurityContract TestCacheControlByPath TestServeFrontendFilesCompressesEachFileOnce
    report frontend-static-routing ./router '^(TestWebFallbackResponseSecurityContract|TestWebFallbackDoesNotCacheMissingAPIOrAssets|TestDisabledWebAssetsServeJSONNotFoundAndKeepPluginDispatcher|TestDisabledWebAssetsTakePrecedenceOverFrontendBaseUrlRedirect|TestDisabledWebAssetsKeepRegisteredAPIRoutes)$' TestWebFallbackResponseSecurityContract TestWebFallbackDoesNotCacheMissingAPIOrAssets TestDisabledWebAssetsServeJSONNotFoundAndKeepPluginDispatcher TestDisabledWebAssetsTakePrecedenceOverFrontendBaseUrlRedirect TestDisabledWebAssetsKeepRegisteredAPIRoutes
    report token-auth-route-expiry ./middleware '^TestUserAuthAppliesAccessTokenRouteRules$' TestUserAuthAppliesAccessTokenRouteRules
    report newapi-balance-finite ./controller '^TestUpdateChannelNewAPIBalanceRejectsNonFiniteConversion$' TestUpdateChannelNewAPIBalanceRejectsNonFiniteConversion
    report token-permissions ./controller '^(TestTokenAllowUserQuotaQueryOnCreateAndUpdate|TestGetTokenUsageFollowsTheUserQuotaSwitchAndIPWhitelist|TestGetTokenRoutableModelsUsesOwnedTokenScopeWithoutCLIApp)$' TestTokenAllowUserQuotaQueryOnCreateAndUpdate TestGetTokenUsageFollowsTheUserQuotaSwitchAndIPWhitelist TestGetTokenRoutableModelsUsesOwnedTokenScopeWithoutCLIApp
    report token-cache-permissions ./model '^(TestTokenAllowUserQuotaQueryRoundTripsThroughRedisHashCache|TestTokenUpdateRevokesCachedUserQuotaQueryPermission|TestRootAffCodeFixtureRejectsRemoteDatabases)$' TestTokenAllowUserQuotaQueryRoundTripsThroughRedisHashCache TestTokenUpdateRevokesCachedUserQuotaQueryPermission TestRootAffCodeFixtureRejectsRemoteDatabases
    report admin-route-authorization ./router '^(TestTokenRoutableModelsRouteRequiresOwnedTokenAndReadScope|TestAccessTokenRouteRulesCoverEveryDashboardRoute|TestChannelStatusRoutesUseExpectedPermissions)$' TestTokenRoutableModelsRouteRequiresOwnedTokenAndReadScope TestAccessTokenRouteRulesCoverEveryDashboardRoute TestChannelStatusRoutesUseExpectedPermissions
    report notification-preferences ./controller '^TestUpdateUserSettingKeepsWhatTheRequestLeavesOut$' TestUpdateUserSettingKeepsWhatTheRequestLeavesOut
    report notification-delivery ./service '^(TestQuotaReminderFiresOncePerDrop|TestChannelNoticesFollowEachAdministratorsSwitch|TestCheckNotificationLimitWindowSpansClockHours|TestCheckNotificationLimitRedisWindowSpansClockHours)$' TestQuotaReminderFiresOncePerDrop TestChannelNoticesFollowEachAdministratorsSwitch TestCheckNotificationLimitWindowSpansClockHours TestCheckNotificationLimitRedisWindowSpansClockHours
    report transactional-email ./controller '^(TestResend|TestSMTPAndLegacyConfigurationNeverUseResend|TestEmailOptionsNeverReturnResendCredentials|TestEmailProvider|TestEmailKeyStatusIsDerivedAndReadOnly|TestSecurityAccountMailsFollowTheOwnersLanguage)' TestResendDeliversThroughSelectedProvider TestResendFailuresDoNotRetryFallbackOrExposeSensitiveContent TestResendRejectsIncompleteOrInvalidConfigurationBeforeSending TestResendRejectsMalformedSuccessAndSanitizesTransportErrors TestSMTPAndLegacyConfigurationNeverUseResend TestEmailOptionsNeverReturnResendCredentials TestEmailProviderOptionRejectsInvalidValues TestEmailProviderCannotActivateIncompleteResend TestResendSettingsChangesKeepActiveDeliveryWorking TestEmailKeyStatusIsDerivedAndReadOnly TestResendPreservesExplicitPlainTextForTransactionalEmails TestSecurityAccountMailsFollowTheOwnersLanguage
    report runtime-resources ./common '^(TestManagedSystemMonitorClearsDisabledStatusAndStopsOnDrain|TestContainer.*|TestSystemOverloadRequiresConsecutiveSamples|TestCheckSystemOverload|TestResendFailuresKeepCredentialsAndEmailContentsOutOfLogs)$' TestManagedSystemMonitorClearsDisabledStatusAndStopsOnDrain TestContainerCPUSaturation TestContainerCPUSaturationFallsBackWithoutQuota TestContainerMemoryUsageUsesWorkingSet TestContainerMemoryUsageFallsBackWhenUnavailable TestSystemOverloadRequiresConsecutiveSamples TestCheckSystemOverload TestResendFailuresKeepCredentialsAndEmailContentsOutOfLogs
    report runtime-drain ./pkg/lifecycle '^(TestForceCancellationIsSeparateFromGracefulDrain|TestWorkerPanicIsReportedAndDoesNotLeakTheJoin|TestWorkerFailureDuringDrainRemainsObservable|TestCompletedDrainWinsOverCancelledWait|TestCompletedWorkerFailureWinsOverCancelledWait)$' TestForceCancellationIsSeparateFromGracefulDrain TestWorkerPanicIsReportedAndDoesNotLeakTheJoin TestWorkerFailureDuringDrainRemainsObservable TestCompletedDrainWinsOverCancelledWait TestCompletedWorkerFailureWinsOverCancelledWait
    prefix=TestTaskSettlementClampPersistence
    report settlement-clamp ./model '^TestTaskSettlementClampPersistence$' "$prefix/positive_infinity" "$prefix/negative_infinity" "$prefix/nan"
    prefix=TestConsumeLogExportsNormalizedCacheUsage
    report dashboard-consume-cache ./model '^TestConsumeLogExportsNormalizedCacheUsage$' "$prefix/normalized_cache_totals" "$prefix/missing_cache_usage" "$prefix/negative_cache_usage"
    report dashboard-separate-log-cache ./model '^TestConsumeLogExportsCacheUsageWithSeparateLogDatabase$' TestConsumeLogExportsCacheUsageWithSeparateLogDatabase
    prefix=TestRelayTaskSubmitRetryWalletDatabase
    report retry ./relay '^TestRelayTaskSubmitRetryWalletDatabase$' "$prefix" "$prefix/higher_quote_reserved_before_paid_request" "$prefix/wallet_shortage_never_calls_retry_upstream" "$prefix/token_shortage_never_calls_retry_upstream" "$prefix/bonus_ineligible_retry_keeps_bonus_untouched" "$prefix/per_call_retry_clears_expression_and_refunds_all"
    prefix=TestRelayTaskSensitiveRejectionPrecedesPluginAndBilling
    report task-sensitive ./relay '^TestRelayTaskSensitiveRejectionPrecedesPluginAndBilling$' "$prefix/json" "$prefix/vendor_json" "$prefix/normalized_vendor_prompt"
    report owner-visibility ./controller '^(TestPublicModelsShareOwnerAndVisibility|TestListModelsPublicOwnerOverridesWithoutLeakingOtherChannels|TestPublicModelDetailPreservesNativeSchemasAndBillingVisibility|TestBuildOpenAIModelUsesGlobalOwnerForStaticAndCustomModels)$' TestPublicModelsShareOwnerAndVisibility TestListModelsPublicOwnerOverridesWithoutLeakingOtherChannels TestPublicModelDetailPreservesNativeSchemasAndBillingVisibility TestBuildOpenAIModelUsesGlobalOwnerForStaticAndCustomModels
    report group-limits ./middleware '^TestModelRateLimitCountsConfiguredGroupsSeparately$' TestModelRateLimitCountsConfiguredGroupsSeparately
    report sensitive-groups ./service '^(TestRequestSensitiveExemptionRequiresAuthorizedActualRoute|TestRequestSensitiveRechecksAutoCrossGroupRetry|TestTaskSensitivePolicyChecksExtractedTextAndPreservesBody|TestRequestSensitiveDisabledSwitchesRemainDisabled)$' TestRequestSensitiveExemptionRequiresAuthorizedActualRoute TestRequestSensitiveRechecksAutoCrossGroupRetry TestTaskSensitivePolicyChecksExtractedTextAndPreservesBody TestRequestSensitiveDisabledSwitchesRemainDisabled
    prefix=TestRequestSensitiveHTTPAndWebSocketBeforeBilling
    required=("$prefix" "$prefix/http/fixed_channel_mismatch")
    for transport in http websocket; do
      for scenario in default_cannot_spoof_group authorized_exemption auto_cross-group_retry auto_token_scope unauthorized_group; do
        required+=("$prefix/$transport/$scenario")
      done
    done
    report sensitive-relay ./controller '^TestRequestSensitiveHTTPAndWebSocketBeforeBilling$' "${required[@]}"
    prefix=TestTaskSensitivePolicyChecksSelectedPluginNormalizationBeforeDriver
    required=()
    for format in json multipart; do
      for scenario in selected_provider_prompt_is_blocked discarded_provider_prompt_does_not_block disabled_policy_still_permits_the_driver; do
        required+=("$prefix/$format/$scenario")
      done
    done
    report sensitive-task-selected-plugin ./controller '^TestTaskSensitivePolicyChecksSelectedPluginNormalizationBeforeDriver$' "${required[@]}"
  fi
  ;;
redis)
  : "${REDIS_ID:?a real Redis service is required}" "${REDIS_PORT:?a real Redis port is required}"
  : "${REDIS_EXPECTED_VERSION:?an exact Redis version is required}"
  if [[ ! "$REDIS_EXPECTED_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo 'An exact numeric Redis release is required.' >&2
    exit 1
  fi
  printf '%s\n' "$REDIS_EXPECTED_VERSION" > "$OUT/redis-expected-version.txt"
  docker inspect --format '{{.Config.Image}} {{.Image}}' "$REDIS_ID" > "$OUT/redis-image.txt"
  docker exec "$REDIS_ID" redis-cli INFO server > "$OUT/redis-version.txt"
  # A requested image tag is not evidence of the server actually exercised.
  redis_version="$(awk '/^redis_version:/ { sub(/\r$/, ""); print }' "$OUT/redis-version.txt")"
  if [ "$redis_version" != "redis_version:$REDIS_EXPECTED_VERSION" ]; then
    echo 'Redis server version is missing, ambiguous, or differs from the required release.' >&2
    exit 1
  fi
  export TEST_RUNTIME_REDIS=1 TEST_REDIS_DSN="redis://127.0.0.1:$REDIS_PORT/0" TEST_REDIS_ADDR="127.0.0.1:$REDIS_PORT"
  report model-script-flush ./middleware '^TestModelRateLimitRealRedis$' TestModelRateLimitRealRedis/reservation_replay_and_expiry TestModelRateLimitRealRedis/script_flush_resumes_request_pipeline
  report token-bucket-script-flush ./common/limiter '^TestAllowFallsBackToEvalAfterScriptFlush$/real_redis$' TestAllowFallsBackToEvalAfterScriptFlush/real_redis
  # The deleted task-batch restart/recovery fixture is retired explicitly.
  # Normal native Redis rate-limit and settlement deadline fixtures remain.
  # The CI-owned Redis service was version-checked above. These opt-ins are
  # scoped to the pause/unpause fixture, never inherited by other reports.
  TEST_SETTLEMENT_DEADLINE_REDIS_ADDR="127.0.0.1:$REDIS_PORT" TEST_SETTLEMENT_DEADLINE_REDIS_DISPOSABLE=1 \
    report settlement-deadline-redis ./service '^TestBillingSettlementDeadlineNativeRedisFence$' TestBillingSettlementDeadlineNativeRedisFence
  ;;
*) echo 'Usage: verify_repair_services.sh database|redis' >&2; exit 1 ;;
esac

if ! git diff --exit-code --ignore-space-at-eol; then status=1; fi
if [ "$status" -ne 0 ]; then
  echo 'Required repair contracts failed; inspect the retained exact-SHA evidence.' >&2
  exit "$status"
fi
printf 'All required %s contracts passed on %s\n' "$1" "$SOURCE_SHA"
