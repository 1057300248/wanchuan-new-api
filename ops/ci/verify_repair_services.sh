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
  case "$DIALECT" in
  mysql)
    : "${MYSQL_ID:?a real MySQL service is required}" "${MYSQL_PORT:?a real MySQL port is required}"
    docker inspect --format '{{.Config.Image}} {{.Image}}' "$MYSQL_ID" > "$OUT/database-image.txt"
    docker exec "$MYSQL_ID" mysql -uroot -pcompat -e 'SELECT VERSION(); ALTER DATABASE repair_compat CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci; CREATE DATABASE repair_compat_log CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;' > "$OUT/database-version.txt"
    export TEST_MYSQL_DSN="root:compat@tcp(127.0.0.1:$MYSQL_PORT)/repair_compat?charset=utf8mb4&parseTime=true&loc=UTC"
    export TEST_MYSQL_LOG_DSN="root:compat@tcp(127.0.0.1:$MYSQL_PORT)/repair_compat_log?charset=utf8mb4&parseTime=true&loc=UTC"
    export TEST_DATABASE_DSN="$TEST_MYSQL_DSN" TEST_LOG_DATABASE_DSN="$TEST_MYSQL_LOG_DSN"
    ;;
  postgres)
    : "${PG_ID:?a real PostgreSQL service is required}" "${PG_PORT:?a real PostgreSQL port is required}"
    docker inspect --format '{{.Config.Image}} {{.Image}}' "$PG_ID" > "$OUT/database-image.txt"
    docker exec "$PG_ID" psql -U postgres -d repair_compat -Atc 'SHOW server_version;' > "$OUT/database-version.txt"
    docker exec "$PG_ID" createdb -U postgres repair_compat_log
    export TEST_POSTGRES_DSN="postgres://postgres:compat@127.0.0.1:$PG_PORT/repair_compat?sslmode=disable"
    export TEST_POSTGRES_LOG_DSN="postgres://postgres:compat@127.0.0.1:$PG_PORT/repair_compat_log?sslmode=disable"
    export TEST_DATABASE_DSN="$TEST_POSTGRES_DSN" TEST_LOG_DATABASE_DSN="$TEST_POSTGRES_LOG_DSN"
    ;;
  sqlite) ;;
  *) echo "Unsupported database dialect: $DIALECT" >&2; exit 1 ;;
  esac

  # Production startup installs an immutable registry; keep it in its own process.
  report startup ./model '^TestDatabaseCompatibilityReal$' TestDatabaseCompatibilityReal
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
  for table in tokens tasks channels balance_ledger; do
    required+=("$prefix/terminal_pending_reload_after_${table}_failure")
  done
  for contract in concurrent_poll_query_single_terminal_refund pending_submit_shortage_then_failure_refunds_held_amount submit_partial_refund_and_terminal_refund_have_distinct_receipts subscription_pending_token_failure_recovers_once pending_queue_rotates_failed_oldest stale_nonterminal_observer_cannot_resurrect_submit_intent submission_saturation_audit_survives_recovery; do
    required+=("$prefix/$contract")
  done
  for token in limited_without_redis limited_with_redis unlimited_without_redis unlimited_with_redis limited_with_ordinary_request; do
    required+=("$prefix/batch_enabled_submission_reservation/$token")
  done
  report wallet ./service "^TestTaskWalletReceiptsDatabaseMatrix$/$DIALECT$" "${required[@]}"
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
  report immediate-settlement ./controller '^TestImmediateTaskSettlementDatabase$' TestImmediateTaskSettlementDatabase TestImmediateTaskSettlementDatabase/accepted_settlement_failure_preserves_task_reference

  if [ "$DIALECT" = sqlite ]; then
    report task-restart ./service '^TestTaskSettlementSQLiteRestartRecovery$' TestTaskSettlementSQLiteRestartRecovery
    prefix=TestTaskBillingBatchReservationRestartRecovery
    report task-batch-restart ./service '^TestTaskBillingBatchReservationRestartRecovery$' "$prefix/limited_without_redis" "$prefix/limited_with_redis" "$prefix/unlimited_without_redis" "$prefix/unlimited_with_redis" "$prefix/limited_with_ordinary_request"
    prefix=TestTaskSettlementClampPersistence
    report settlement-clamp ./model '^TestTaskSettlementClampPersistence$' "$prefix/positive_infinity" "$prefix/negative_infinity" "$prefix/nan"
    prefix=TestConsumeLogExportsNormalizedCacheUsage
    report dashboard-consume-cache ./model '^TestConsumeLogExportsNormalizedCacheUsage$' "$prefix/normalized_cache_totals" "$prefix/missing_cache_usage" "$prefix/negative_cache_usage"
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
  docker inspect --format '{{.Config.Image}} {{.Image}}' "$REDIS_ID" > "$OUT/redis-image.txt"
  docker exec "$REDIS_ID" redis-cli INFO server > "$OUT/redis-version.txt"
  export TEST_RUNTIME_REDIS=1 TEST_REDIS_DSN="redis://127.0.0.1:$REDIS_PORT/0" TEST_REDIS_ADDR="127.0.0.1:$REDIS_PORT"
  report model-script-flush ./middleware '^TestModelRateLimitRealRedis$' TestModelRateLimitRealRedis/reservation_replay_and_expiry TestModelRateLimitRealRedis/script_flush_resumes_request_pipeline
  report token-bucket-script-flush ./common/limiter '^TestAllowFallsBackToEvalAfterScriptFlush$/real_redis$' TestAllowFallsBackToEvalAfterScriptFlush/real_redis
  export TEST_TASK_REDIS_ADDR="127.0.0.1:$REDIS_PORT"
  prefix=TestTaskBillingBatchReservationRestartRecovery
  report task-token-reservation ./service '^TestTaskBillingBatchReservationRestartRecovery$' "$prefix/limited_with_redis" "$prefix/unlimited_with_redis" "$prefix/limited_with_ordinary_request"
  ;;
*) echo 'Usage: verify_repair_services.sh database|redis' >&2; exit 1 ;;
esac

if ! git diff --exit-code --ignore-space-at-eol; then status=1; fi
if [ "$status" -ne 0 ]; then
  echo 'Required repair contracts failed; inspect the retained exact-SHA evidence.' >&2
  exit "$status"
fi
printf 'All required %s contracts passed on %s\n' "$1" "$SOURCE_SHA"
