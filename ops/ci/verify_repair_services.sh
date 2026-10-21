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
  # The finalization worker scans every pending intent. Never share its fixture
  # database between model/service suites or inherit an ambient database DSN.
  export TEST_FINALIZATION_MODEL_MYSQL_DSN= TEST_FINALIZATION_MODEL_POSTGRES_DSN=
  export TEST_FINALIZATION_SERVICE_MYSQL_DSN= TEST_FINALIZATION_SERVICE_POSTGRES_DSN=
  finalization_model_mysql_dsn= finalization_model_postgres_dsn=
  finalization_service_mysql_dsn= finalization_service_postgres_dsn=
  refund_operations_mysql_dsn= refund_operations_postgres_dsn=
  case "$DIALECT" in
  mysql)
    : "${MYSQL_ID:?a real MySQL service is required}" "${MYSQL_PORT:?a real MySQL port is required}"
    docker inspect --format '{{.Config.Image}} {{.Image}}' "$MYSQL_ID" > "$OUT/database-image.txt"
    docker exec "$MYSQL_ID" mysql -uroot -pcompat -e 'SELECT VERSION(); ALTER DATABASE repair_compat CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci; CREATE DATABASE repair_compat_log CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci; CREATE DATABASE repair_token_migration CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;' > "$OUT/database-version.txt"
    docker exec "$MYSQL_ID" mysql -uroot -pcompat -e 'CREATE DATABASE repair_finalization_model CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci; CREATE DATABASE repair_finalization_service CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;'
    docker exec "$MYSQL_ID" mysql -uroot -pcompat -e 'CREATE DATABASE repair_refund_operations CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;'
    refund_operations_mysql_dsn="root:compat@tcp(127.0.0.1:$MYSQL_PORT)/repair_refund_operations?charset=utf8mb4&parseTime=true&loc=UTC"
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
    docker exec "$PG_ID" createdb -U postgres repair_refund_operations
    refund_operations_postgres_dsn="postgres://postgres:compat@127.0.0.1:$PG_PORT/repair_refund_operations?sslmode=disable"
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
  for table in tokens tasks channels balance_ledger; do
    required+=("$prefix/terminal_pending_reload_after_${table}_failure")
  done
  for contract in concurrent_poll_query_single_terminal_refund pending_submit_shortage_then_failure_refunds_held_amount submit_partial_refund_and_terminal_refund_have_distinct_receipts subscription_pending_token_failure_recovers_once pending_queue_rotates_failed_oldest stale_nonterminal_observer_cannot_resurrect_submit_intent submission_saturation_audit_survives_recovery; do
    required+=("$prefix/$contract")
  done
  for token in limited_without_redis limited_with_redis unlimited_without_redis unlimited_with_redis limited_with_ordinary_request; do
    required+=("$prefix/batch_enabled_submission_reservation/$token")
  done
  for contract in late_submit late_terminal_refund late_terminal_positive unknown_legacy_task settled_source_pending_task live_session_refund live_session_reset_refund wallet_session_clears_period; do
    required+=("$prefix/subscription_period_$contract")
  done
  report wallet ./service "^TestTaskWalletReceiptsDatabaseMatrix$/$DIALECT$" "${required[@]}"
  prefix="TestSubscriptionPeriodAccountingDatabaseMatrix/$DIALECT"
  required=("$prefix")
  for contract in same_period_settle same_period_upward same_period_zero same_period_refund quota_ceiling journal_identity preconsume_replay parallel_preconsume auto_reset_settle auto_reset_refund manual_reset_settle same_second_reset_refund old_positive_settle reserve_closed_period legacy_unknown deleted_source expired_source underflow parallel_settle parallel_refund reset_races_refund; do
    required+=("$prefix/$contract")
  done
  report subscription-period ./model "^TestSubscriptionPeriodAccountingDatabaseMatrix$/$DIALECT$" "${required[@]}"
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
  report immediate-settlement ./controller '^TestImmediateTaskSettlementDatabase$' TestImmediateTaskSettlementDatabase TestImmediateTaskSettlementDatabase/accepted_settlement_failure_preserves_task_reference


  # Required leaf inventory is reviewed and mirrored by offline orchestration tests.
  prefix="TestBillingFinalizationDatabaseMatrix/$DIALECT"
  required=("$prefix")
  for contract in \
    'atomic_failure_stages/ledger' \
    'atomic_failure_stages/operation' \
    'atomic_failure_stages/source' \
    'atomic_failure_stages/terminal' \
    'atomic_failure_stages/token' \
    'concurrent_workers_and_first_owners' \
    'migration_preserves_pending_and_tombstones' \
    'near_miss_realtime_key_rejected/wss:+1:20' \
    'near_miss_realtime_key_rejected/wss:01:20' \
    'near_miss_realtime_key_rejected/wss:0:20' \
    'near_miss_realtime_key_rejected/wss:18446744073709551616:20' \
    'near_miss_realtime_key_rejected/wss:1:21' \
    'original_token_identity/hard_deleted' \
    'original_token_identity/no_token' \
    'original_token_identity/replacement_key' \
    'original_token_identity/replacement_owner' \
    'original_token_identity/soft_deleted' \
    'original_token_identity/unlimited' \
    'receipt_mismatch_and_legacy_replay' \
    'rollback_probe_no_permanent_owner' \
    'subscription_original_period/cumulative' \
    'subscription_original_period/missing_subscription' \
    'subscription_original_period/period_reset' \
    'subscription_original_period/receipt_changed' \
    'subscription_original_period/unknown_period' \
    'task_owner_and_evolved_quota' \
    'task_transfer_rollback_and_refund_race/insert' \
    'task_transfer_rollback_and_refund_race/owner'; do
    required+=("$prefix/$contract")
  done
  TEST_FINALIZATION_MODEL_MYSQL_DSN="$finalization_model_mysql_dsn" TEST_FINALIZATION_MODEL_POSTGRES_DSN="$finalization_model_postgres_dsn" \
    report request-finalization-model ./model "^TestBillingFinalizationDatabaseMatrix$/$DIALECT$" "${required[@]}"
  prefix="TestBillingFinalizationSessionDatabaseMatrix/$DIALECT"
  required=("$prefix")
  for contract in \
    'commit_boundaries/lost_ack/refund_intent' \
    'commit_boundaries/lost_ack/settlement' \
    'commit_boundaries/lost_ack/task_insert' \
    'commit_boundaries/rollback/refund_intent' \
    'commit_boundaries/rollback/settlement' \
    'commit_boundaries/rollback/task_insert' \
    'extension_capability/custom_billing_settler_insert' \
    'extension_capability/custom_non_wallet' \
    'extension_capability/custom_subscription_delta' \
    'extension_capability/custom_wallet_atomic_replay' \
    'failed_settle_reserve_refund' \
    'independent_violation_fee/contradictory_marker' \
    'independent_violation_fee/fee_before_worker' \
    'independent_violation_fee/unmarked_same_key_replay' \
    'independent_violation_fee/worker_before_fee' \
    'intent_failure_admission' \
    'legacy_final_more_less/a' \
    'legacy_final_more_less/Å' \
    'legacy_final_more_less/ĕ' \
    'probe_failure' \
    'real_session_partial_less' \
    'real_session_partial_more' \
    'real_session_playground' \
    'real_session_subscription_refund' \
    'real_session_subscription_reset_refund' \
    'real_session_subscription_task_handoff' \
    'real_session_subscription_zero_delta' \
    'real_session_subscription_zero_delta_replaced_token' \
    'real_session_task_handoff' \
    'real_session_trusted' \
    'real_session_unlimited' \
    'realtime_increment_terminal/failed_final_then_refund' \
    'realtime_increment_terminal/final_settle' \
    'realtime_increment_terminal/refund_initial_hold' \
    'refund_commit_boundaries/lost_ack' \
    'refund_commit_boundaries/rollback' \
    'separate_held_amounts' \
    'source_failure_restart' \
    'zero_delta_replacement/hold_0/key' \
    'zero_delta_replacement/hold_0/owner' \
    'zero_delta_replacement/hold_100/key' \
    'zero_delta_replacement/hold_100/owner' \
    'intent_deadline_cancelled_request' \
    'intent_bounded_admission/queue_full' \
    'intent_bounded_admission/sealed' \
    'intent_bounded_admission/database_busy' \
    'intent_active_job_capacity' \
    'intent_shutdown_gap' \
    'intent_drain_retry' \
    'intent_retry_terminal_owner' \
    'intent_released_session' \
    'task_commit_readback_outage/rolled_back' \
    'task_commit_readback_outage/committed' \
    'task_reconciliation_identity/user' \
    'task_reconciliation_identity/token' \
    'task_reconciliation_identity/fingerprint' \
    'task_reconciliation_identity/held' \
    'task_reconciliation_identity/token_held' \
    'task_reconciliation_identity/preconsume' \
    'task_reconciliation_identity/source_period' \
    'task_reconciliation_identity/subscription_period' \
    'task_reconciliation_identity/task_identity' \
    'task_reconciliation_identity/unsafe_status' \
    'task_reconciliation_identity/missing_destination' \
    'task_reconciliation_identity/other_terminal_owner'; do
    required+=("$prefix/$contract")
  done
  TEST_FINALIZATION_SERVICE_MYSQL_DSN="$finalization_service_mysql_dsn" TEST_FINALIZATION_SERVICE_POSTGRES_DSN="$finalization_service_postgres_dsn" \
    report request-finalization-service ./service "^TestBillingFinalizationSessionDatabaseMatrix$/$DIALECT$" "${required[@]}"

  # The operations fixture refuses populated databases, upgrades the exact old
  # receipt schema, and retains every poison-row hold while healthy work advances.
  prefix="TestBillingRefundOperationsDatabaseMatrix/$DIALECT"
  required=(TestBillingRefundOperationsDatabaseMatrix "$prefix")
  for mode in upgrade fresh; do
    required+=("$prefix/$mode")
    for contract in poison_prefix_does_not_starve_refunds new_intents_do_not_starve_older_due_retry failure_history_clock_and_metadata_failure; do
      required+=("$prefix/$mode/$contract")
    done
    if [ "$DIALECT" != sqlite ]; then
      required+=("$prefix/$mode/held_source_lock_respects_deadline")
    fi
  done
  required+=("$prefix/long_pending_queue_pressure")
  TEST_REFUND_OPS_MYSQL_DSN="$refund_operations_mysql_dsn" TEST_REFUND_OPS_POSTGRES_DSN="$refund_operations_postgres_dsn" TEST_REFUND_QUEUE_PRESSURE=1 \
    report refund-operations ./model "^TestBillingRefundOperationsDatabaseMatrix$/$DIALECT$" "${required[@]}"

  if [ "$DIALECT" = sqlite ]; then
    # Enumerate intent tests: the subprocess-only helper must not run standalone.
    required=(
      'TestBillingRefundIntentDeadlineSurvivesRequestCancellation'
      'TestBillingRefundIntentSaturationAndSealedGroup/queue_full'
      'TestBillingRefundIntentSaturationAndSealedGroup/sealed'
      'TestBillingRefundIntentSaturationAndSealedGroup/database_busy'
      'TestBillingRefundIntentSaturationAndSealedGroup'
      'TestBillingRefundIntentActiveJobSaturatesWithoutExtraDatabaseWork'
      'TestBillingRefundIntentShutdownKeepsAbsentDecisionUnrecoverable'
      'TestBillingRefundIntentDrainFinishesAcceptedRetry'
      'TestBillingRefundIntentRetryHonorsCommittedTerminalOwner'
      'TestBillingRefundIntentReportBoundsUnvalidatedIdentity'
      'TestBillingRefundIntentOutlivesReleasedSessionWithoutRetainingRequest'
      'TestBillingRefundIntentProcessExit/before_intent'
      'TestBillingRefundIntentProcessExit/after_retry_commits_intent'
      'TestBillingRefundIntentProcessExit'
    )
    report refund-intent-process ./service '^(TestBillingRefundIntentDeadlineSurvivesRequestCancellation|TestBillingRefundIntentSaturationAndSealedGroup|TestBillingRefundIntentActiveJobSaturatesWithoutExtraDatabaseWork|TestBillingRefundIntentShutdownKeepsAbsentDecisionUnrecoverable|TestBillingRefundIntentDrainFinishesAcceptedRetry|TestBillingRefundIntentRetryHonorsCommittedTerminalOwner|TestBillingRefundIntentReportBoundsUnvalidatedIdentity|TestBillingRefundIntentOutlivesReleasedSessionWithoutRetainingRequest|TestBillingRefundIntentProcessExit)$' "${required[@]}"
    required=(
      'TestTaskSubmissionUncertainPersistenceClientContracts/rolled_back/native'
      'TestTaskSubmissionUncertainPersistenceClientContracts/rolled_back/native_plugin'
      'TestTaskSubmissionUncertainPersistenceClientContracts/rolled_back/images'
      'TestTaskSubmissionUncertainPersistenceClientContracts/rolled_back/responses'
      'TestTaskSubmissionUncertainPersistenceClientContracts/rolled_back/video'
      'TestTaskSubmissionUncertainPersistenceClientContracts/committed/native'
      'TestTaskSubmissionUncertainPersistenceClientContracts/committed/native_plugin'
      'TestTaskSubmissionUncertainPersistenceClientContracts/committed/images'
      'TestTaskSubmissionUncertainPersistenceClientContracts/committed/responses'
      'TestTaskSubmissionUncertainPersistenceClientContracts/committed/video'
      'TestTaskSubmissionUncertainPersistenceClientContracts'
    )
    report task-persistence-presenter ./controller '^(TestTaskSubmissionUncertainPersistenceClientContracts)$' "${required[@]}"
    required=(
      'TestBillingRefundDeadlineIncludesTokenFenceAndUserCache/postcommit_user_cache'
      'TestBillingRefundDeadlineIncludesTokenFenceAndUserCache/precommit_token_fence'
      'TestBillingRefundDeadlineIncludesTokenFenceAndUserCache/expired_deadline_before_notification'
      'TestBillingRefundDeadlineIncludesTokenFenceAndUserCache'
      'TestBillingRefundContextPreservesTokenFence'
      'TestBillingRefundReplayInvalidatesUserCache'
      'TestBillingRefundRedisErrorRetainsLegacyLiveContextPolicy'
      'TestBillingRefundRetryClassificationAndSchedule/unknown_secret'
      'TestBillingRefundRetryClassificationAndSchedule/timeout'
      'TestBillingRefundRetryClassificationAndSchedule/cancel'
      'TestBillingRefundRetryClassificationAndSchedule/identity'
      'TestBillingRefundRetryClassificationAndSchedule/missing'
      'TestBillingRefundRetryClassificationAndSchedule/period_unknown'
      'TestBillingRefundRetryClassificationAndSchedule/metadata'
      'TestBillingRefundRetryClassificationAndSchedule'
      'TestBillingRefundDueRecheckDoesNotBypassSchedule'
    )
    report refund-deadline-classifier ./model '^(TestBillingRefundDeadlineIncludesTokenFenceAndUserCache|TestBillingRefundContextPreservesTokenFence|TestBillingRefundReplayInvalidatesUserCache|TestBillingRefundRedisErrorRetainsLegacyLiveContextPolicy|TestBillingRefundRetryClassificationAndSchedule|TestBillingRefundDueRecheckDoesNotBypassSchedule)$' "${required[@]}"
    report subscription-period-replay ./model '^(TestSubscriptionPeriodTransactionRollback|TestSubscriptionPeriodReplayRejectsStaleConsumedObservation|TestSubscriptionPeriodJournalReserveSettlementValidation|TestSubscriptionPeriodReplayAfterAbsentSnapshot|TestSubscriptionRefundCommitsReceiptAndUsageTogether)$' TestSubscriptionPeriodTransactionRollback TestSubscriptionPeriodReplayRejectsStaleConsumedObservation TestSubscriptionPeriodJournalReserveSettlementValidation TestSubscriptionPeriodReplayAfterAbsentSnapshot TestSubscriptionRefundCommitsReceiptAndUsageTogether
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
    report task-restart ./service '^TestTaskSettlementSQLiteRestartRecovery$' TestTaskSettlementSQLiteRestartRecovery
    prefix=TestTaskBillingBatchReservationRestartRecovery
    report task-batch-restart ./service '^TestTaskBillingBatchReservationRestartRecovery$' "$prefix/limited_without_redis" "$prefix/limited_with_redis" "$prefix/unlimited_without_redis" "$prefix/unlimited_with_redis" "$prefix/limited_with_ordinary_request"
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
