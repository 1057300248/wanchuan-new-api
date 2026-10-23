package model

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
)

const host093OldRequest = "release-upgrade-subscription-request"
const host093NewRequest = "release-upgrade-new-subscription-request"

// This root runs in two separate, strictly selected test processes. The owned
// supervisor must run and join the real C master before EACH phase. This test
// neither seeds the old release nor registers or migrates a replacement host.
func TestReleaseUpgradeHostVerification(t *testing.T) {
	phase := os.Getenv("TEST_RELEASE_HOST_PHASE")
	if phase == "" {
		require.Empty(t, os.Getenv("TEST_RELEASE_HOST_PG"), "opted-in verifier requires a phase")
		require.Empty(t, os.Getenv("TEST_RELEASE_HOST_DISPOSABLE"), "opted-in verifier requires a phase")
		t.Skip("owned PostgreSQL host-upgrade phase not configured")
	}
	require.Contains(t, []string{"reserve", "recheck"}, phase)
	require.Equal(t, "1", os.Getenv("TEST_RELEASE_HOST_PG"))
	require.Equal(t, "1", os.Getenv("TEST_RELEASE_HOST_DISPOSABLE"))
	binding := host093Binding(t)
	baselinePath := host093BaselinePath(t)
	var saved host093Baseline
	if phase == "reserve" {
		_, err := os.Lstat(baselinePath)
		require.True(t, os.IsNotExist(err), "reserve requires a new baseline path")
		require.Empty(t, os.Getenv("TEST_RELEASE_HOST_BASELINE_SHA256"))
	} else {
		saved = host093ReadBaseline(t, baselinePath)
		require.Equal(t, binding, saved.Binding)
	}

	mainDSN, mainConfig := host093DSN(t, "SQL_DSN", "repair_release_main")
	logDSN, logConfig := host093DSN(t, "LOG_SQL_DSN", "repair_release_log")
	require.Equal(t, mainConfig.Host, logConfig.Host)
	require.Equal(t, mainConfig.Port, logConfig.Port)
	require.Equal(t, mainConfig.User, logConfig.User)
	endpoint := net.JoinHostPort(mainConfig.Host, strconv.Itoa(int(mainConfig.Port)))
	if phase == "recheck" {
		require.Equal(t, endpoint, saved.Endpoint)
	}
	// These connectors are read-only at the PostgreSQL session level. Missing
	// host-created tables/columns/deadline fail BEFORE InitDB can write anything.
	mainProbe := host093Probe(t, mainConfig)
	logProbe := host093Probe(t, logConfig)
	host093Schema(t, mainProbe, logProbe)
	host093Deadline(t, mainProbe, binding.Deadline)
	beforeAttach := host093Snapshots(t, mainProbe, logProbe)

	previousDB, previousLog := DB, LOG_DB
	previousMaster, previousRedis, previousBatch := common.IsMasterNode, common.RedisEnabled, common.BatchUpdateEnabled
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousDeadline := legacyAccessTokenRetireAt.Load()
	common.IsMasterNode, common.RedisEnabled, common.BatchUpdateEnabled = false, false, false
	// Register cleanup before either open, including partial InitLogDB failure.
	t.Cleanup(func() {
		var closeErrors []error
		if LOG_DB != nil && LOG_DB != previousLog && LOG_DB != DB {
			closeErrors = append(closeErrors, closeDB(LOG_DB))
		}
		if DB != nil && DB != previousDB {
			closeErrors = append(closeErrors, closeDB(DB))
		}
		DB, LOG_DB = previousDB, previousLog
		common.IsMasterNode, common.RedisEnabled, common.BatchUpdateEnabled = previousMaster, previousRedis, previousBatch
		common.SetDatabaseTypes(previousMainType, previousLogType)
		legacyAccessTokenRetireAt.Store(previousDeadline)
		initCol()
		for _, err := range closeErrors {
			require.NoError(t, err)
		}
	})
	t.Setenv("SQL_DSN", mainDSN)
	t.Setenv("LOG_SQL_DSN", logDSN)
	t.Setenv("SQL_MAX_IDLE_CONNS", "1")
	t.Setenv("SQL_MAX_OPEN_CONNS", "2")
	require.NoError(t, InitDB())
	require.NoError(t, InitLogDB())
	require.Equal(t, common.DatabaseTypePostgreSQL, common.MainDatabaseType())
	require.Equal(t, common.DatabaseTypePostgreSQL, common.LogDatabaseType())
	host093Deadline(t, mainProbe, binding.Deadline)
	require.Equal(t, beforeAttach, host093Snapshots(t, mainProbe, logProbe), "non-migrating attach must not repair fixture rows")

	user, token, subscription, oldReceipt := host093AssertSeed(t, phase)
	mainSQL, err := DB.DB()
	require.NoError(t, err)
	logSQL, err := LOG_DB.DB()
	require.NoError(t, err)
	host093Uniqueness(t, mainSQL, logSQL)
	if phase == "reserve" {
		var count int64
		require.NoError(t, DB.Model(&SubscriptionPreConsumeRecord{}).Where("request_id = ?", host093NewRequest).Count(&count).Error)
		require.Zero(t, count, "reserve is exactly once, not a replay")
		reservation, err := PreConsumeUserSubscription(host093NewRequest, user.Id, "release-upgrade-model", 0, 100)
		require.NoError(t, err)
		require.False(t, reservation.Replayed)
		require.Equal(t, subscription.Id, reservation.UserSubscriptionId)
		require.EqualValues(t, 350, reservation.AmountUsedBefore)
		require.EqualValues(t, 450, reservation.AmountUsedAfter)
		require.EqualValues(t, 100, reservation.ReservedAmount)
		var afterOld SubscriptionPreConsumeRecord
		var afterUser User
		var afterToken Token
		require.NoError(t, DB.First(&afterOld, oldReceipt.Id).Error)
		require.NoError(t, DB.First(&afterUser, user.Id).Error)
		require.NoError(t, DB.First(&afterToken, token.Id).Error)
		require.Equal(t, oldReceipt, afterOld, "all historical receipt fields stay unchanged")
		// Avoid printing complete identity objects in failure reports.
		require.True(t, reflect.DeepEqual(user, afterUser), "subscription reservation changed user identity/wallet")
		require.True(t, reflect.DeepEqual(token, afterToken), "subscription reservation changed token")
	}
	var currentSubscription UserSubscription
	var fresh SubscriptionPreConsumeRecord
	require.NoError(t, DB.First(&currentSubscription, subscription.Id).Error)
	require.NoError(t, DB.Where("request_id = ?", host093NewRequest).First(&fresh).Error)
	require.EqualValues(t, 450, currentSubscription.AmountUsed)
	require.Positive(t, currentSubscription.QuotaPeriod)
	require.Equal(t, currentSubscription.QuotaPeriod, fresh.QuotaPeriod)
	require.Equal(t, user.Id, fresh.UserId)
	require.Equal(t, subscription.Id, fresh.UserSubscriptionId)
	require.EqualValues(t, 100, fresh.PreConsumed)
	require.EqualValues(t, 100, fresh.ReservedAmount)
	require.Zero(t, fresh.SettledAmount)
	require.Equal(t, "consumed", fresh.Status)
	host093Deadline(t, mainProbe, binding.Deadline)
	after := host093Snapshots(t, mainProbe, logProbe)
	if phase == "reserve" {
		for name, digest := range beforeAttach {
			if name != "subscription" && name != "fresh_receipt" {
				require.Equal(t, digest, after[name], "protected full-row snapshot: %s", name)
			}
		}
		host093WriteBaseline(t, baselinePath, host093Baseline{Schema: 1, Phase: "reserved", Binding: binding, Endpoint: endpoint, Snapshots: after})
	} else {
		// No second reservation, refund, settlement or model migration is run.
		require.Equal(t, saved.Snapshots, beforeAttach, "real master restart changed reserved baseline")
		require.Equal(t, saved.Snapshots, after, "recheck changed protected fixture rows")
	}
}

type host093SourceBinding struct {
	Source   string `json:"source"`
	Tree     string `json:"tree"`
	RunID    string `json:"run_id"`
	Deadline string `json:"legacy_access_token_retire_at"`
}

type host093Baseline struct {
	Schema    int                  `json:"schema"`
	Phase     string               `json:"phase"`
	Binding   host093SourceBinding `json:"binding"`
	Endpoint  string               `json:"endpoint"`
	Snapshots map[string]string    `json:"snapshots"`
}

func host093Binding(t *testing.T) host093SourceBinding {
	t.Helper()
	binding := host093SourceBinding{Source: os.Getenv("TEST_RELEASE_HOST_SOURCE"), Tree: os.Getenv("TEST_RELEASE_HOST_TREE"), RunID: os.Getenv("TEST_RELEASE_HOST_RUN_ID"), Deadline: os.Getenv("TEST_RELEASE_HOST_DEADLINE")}
	for _, value := range []string{binding.Source, binding.Tree} {
		require.Regexp(t, `^[0-9a-f]{40}$`, value)
	}
	require.Regexp(t, `^[0-9a-f]{32}$`, binding.RunID)
	deadline, err := strconv.ParseInt(binding.Deadline, 10, 64)
	require.NoError(t, err)
	require.Positive(t, deadline)
	require.Equal(t, strconv.FormatInt(deadline, 10), binding.Deadline)
	return binding
}

func host093DSN(t *testing.T, env, database string) (string, *pgx.ConnConfig) {
	t.Helper()
	raw := os.Getenv(env)
	require.NotEmpty(t, raw, "%s is required", env)
	u, err := url.Parse(raw)
	require.True(t, err == nil, "%s must be a PostgreSQL URL", env)
	require.Contains(t, []string{"postgres", "postgresql"}, u.Scheme)
	require.Empty(t, u.Fragment)
	require.Equal(t, "/"+database, u.Path)
	ip := net.ParseIP(u.Hostname())
	require.NotNil(t, ip, "literal loopback IP required")
	require.True(t, ip.IsLoopback())
	port, err := strconv.ParseUint(u.Port(), 10, 16)
	require.NoError(t, err)
	require.Positive(t, port)
	query, err := url.ParseQuery(u.RawQuery)
	require.NoError(t, err)
	for key, values := range query {
		require.Contains(t, []string{"sslmode", "connect_timeout", "application_name", "statement_timeout", "lock_timeout", "search_path"}, key)
		require.Len(t, values, 1)
	}
	require.Equal(t, "disable", query.Get("sslmode"))
	query.Set("connect_timeout", "2")
	query.Set("statement_timeout", "5000")
	query.Set("lock_timeout", "2000")
	query.Set("search_path", "public")
	u.RawQuery = query.Encode()
	config, err := pgx.ParseConfig(u.String())
	require.True(t, err == nil, "invalid bounded PostgreSQL config")
	require.Equal(t, u.Hostname(), config.Host)
	require.EqualValues(t, port, config.Port)
	require.Equal(t, database, config.Database)
	require.Nil(t, config.TLSConfig)
	require.Empty(t, config.Fallbacks)
	config.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	return u.String(), config
}

func host093Probe(t *testing.T, config *pgx.ConnConfig) *sql.DB {
	t.Helper()
	readConfig := config.Copy()
	readConfig.RuntimeParams["default_transaction_read_only"] = "on"
	pool := stdlib.OpenDB(*readConfig)
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var version, database, host, user, schema, readOnly string
	var port int
	err := pool.QueryRowContext(ctx, "SELECT current_setting('server_version'), current_database(), inet_server_addr()::text, inet_server_port(), current_user, current_schema(), current_setting('transaction_read_only')").Scan(&version, &database, &host, &port, &user, &schema, &readOnly)
	require.NoError(t, err)
	versionFields := strings.Fields(version)
	require.NotEmpty(t, versionFields)
	require.Equal(t, "18.6", versionFields[0])
	require.Equal(t, config.Database, database)
	require.True(t, net.ParseIP(host).Equal(net.ParseIP(config.Host)))
	require.EqualValues(t, config.Port, port)
	require.Equal(t, config.User, user)
	require.Equal(t, "public", schema)
	require.Equal(t, "on", readOnly)
	return pool
}

func host093Schema(t *testing.T, main, log *sql.DB) {
	t.Helper()
	for _, target := range []struct {
		db      *sql.DB
		columns map[string][]string
	}{
		{main, map[string][]string{
			"users": {"quota", "quota_paid", "quota_bonus", "quota_reward", "auth_version", "access_token_created_at"},
			"tokens": {"allow_user_quota_query"}, "options": {"key", "value"},
			"user_subscriptions": {"quota_period", "amount_used", "next_reset_time"},
			"subscription_plans": {"quota_reset_period"},
			"subscription_pre_consume_records": {"quota_period", "reserved_amount", "settled_amount"},
			"midjourneys": {"wallet_debit"}, "billing_finalizations": {"request_id"},
			"balance_ledger": {"id"}, "balance_ledger_operations": {"id"}, "models": {"model_name"},
		}},
		{log, map[string][]string{"logs": {"request_id"}, "audit_logs": {"event_id", "other"}}},
	} {
		for table, columns := range target.columns {
			for _, column := range columns {
				var count int
				err := target.db.QueryRow("SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 AND column_name=$2", table, column).Scan(&count)
				require.NoError(t, err)
				require.Equal(t, 1, count, "host did not create %s.%s", table, column)
			}
		}
	}
}

func host093Deadline(t *testing.T, db *sql.DB, expected string) {
	t.Helper()
	var value string
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*), COALESCE(min(value),'') FROM public.options WHERE key='LegacyAccessTokenRetireAt'`).Scan(&count, &value))
	require.Equal(t, 1, count, "deadline must exist before slave InitDB")
	require.Equal(t, expected, value, "host deadline must not be synthesized or changed")
}

// Assertions are adapted from donor 26eb554's upgrade verification branch.
// They inspect the exact released seed, not a hand-built candidate replacement.
func host093AssertSeed(t *testing.T, phase string) (User, Token, UserSubscription, SubscriptionPreConsumeRecord) {
	t.Helper()
	var user User
	require.NoError(t, DB.Where("username = ?", "release-upgrade-owner").First(&user).Error)
	require.Equal(t, 5000, user.Quota)
	require.Equal(t, 4000, user.QuotaPaid)
	require.Equal(t, 1000, user.QuotaBonus)
	require.Zero(t, user.QuotaReward)
	require.EqualValues(t, 4294967400, user.UsedQuota)
	require.EqualValues(t, 7, user.AuthVersion)
	require.True(t, user.Password == "existing-password-hash", "released password identity changed")
	require.NotNil(t, user.AccessToken)
	require.True(t, strings.TrimRight(*user.AccessToken, " ") == "release-upgrade-access-token", "released access token identity changed")
	require.Equal(t, "release-upgrade-aff", user.AffCode)
	var token Token
	require.NoError(t, DB.Where("user_id = ? AND name = ?", user.Id, "release-upgrade-token").First(&token).Error)
	require.True(t, token.Key == strings.Repeat("k", 48), "released API token identity changed")
	require.Equal(t, common.TokenStatusEnabled, token.Status)
	require.EqualValues(t, -1, token.ExpiredTime)
	require.Equal(t, "default", token.Group)
	require.Equal(t, 700, token.RemainQuota)
	require.Equal(t, 300, token.UsedQuota)
	require.True(t, token.ModelLimitsEnabled)
	require.Equal(t, "release-upgrade-model", token.ModelLimits)
	require.False(t, token.AllowUserQuotaQuery, "old token must not gain wallet-query permission")
	var plan SubscriptionPlan
	require.NoError(t, DB.Where("title = ?", "release-upgrade-subscription").First(&plan).Error)
	require.Equal(t, SubscriptionResetNever, plan.QuotaResetPeriod)
	var subscription UserSubscription
	require.NoError(t, DB.Where("user_id = ? AND plan_id = ?", user.Id, plan.Id).First(&subscription).Error)
	require.EqualValues(t, 1000, subscription.AmountTotal)
	if phase == "reserve" {
		require.EqualValues(t, 350, subscription.AmountUsed)
		require.Zero(t, subscription.QuotaPeriod)
	} else {
		require.EqualValues(t, 450, subscription.AmountUsed)
		require.Positive(t, subscription.QuotaPeriod)
	}
	require.EqualValues(t, 1788600000, subscription.StartTime)
	require.EqualValues(t, 4102444800, subscription.EndTime)
	require.Zero(t, subscription.NextResetTime)
	require.Equal(t, "active", subscription.Status)
	require.True(t, subscription.AllowWalletOverflow)
	var old SubscriptionPreConsumeRecord
	require.NoError(t, DB.Where("request_id = ?", host093OldRequest).First(&old).Error)
	require.Equal(t, user.Id, old.UserId)
	require.Equal(t, subscription.Id, old.UserSubscriptionId)
	require.EqualValues(t, 200, old.PreConsumed)
	require.Equal(t, "consumed", old.Status)
	require.Zero(t, old.QuotaPeriod)
	require.Zero(t, old.ReservedAmount)
	require.Zero(t, old.SettledAmount)
	var mj Midjourney
	require.NoError(t, DB.Where("user_id = ? AND mj_id = ?", user.Id, "release-upgrade-mj").First(&mj).Error)
	require.Equal(t, "IMAGINE", mj.Action)
	require.Equal(t, "IN_PROGRESS", mj.Status)
	require.Equal(t, "50%", mj.Progress)
	require.Equal(t, 300, mj.Quota)
	require.Equal(t, token.Id, mj.TokenId)
	require.Equal(t, 19, mj.ChannelId)
	require.Equal(t, 23, mj.BillingChannelId)
	require.Equal(t, "historical Midjourney prompt", mj.Prompt)
	require.EqualValues(t, 1788600000, mj.SubmitTime)
	var walletDebit sql.NullString
	require.NoError(t, DB.Table("midjourneys").Select("wallet_debit").Where("id = ?", mj.Id).Row().Scan(&walletDebit))
	require.False(t, walletDebit.Valid, "old MJ keeps its NULL debit receipt")
	for _, column := range []string{"user_id", "action", "mj_id", "submit_time", "start_time", "finish_time", "status", "progress"} {
		require.True(t, DB.Migrator().HasIndex("midjourneys", DB.NamingStrategy.IndexName("midjourneys", column)), "missing MJ index: %s", column)
	}
	var metadata Model
	require.NoError(t, DB.Where("model_name = ?", "release-upgrade-model").First(&metadata).Error)
	require.Equal(t, common.GetPointer(128000), metadata.ContextLength)
	require.Equal(t, `["vision"]`, metadata.Capabilities)
	require.Equal(t, common.GetPointer(false), metadata.BonusEligible)
	require.Zero(t, metadata.SyncOfficial)
	var option Option
	require.NoError(t, DB.Where(commonKeyCol+" = ?", "ModelRatio").First(&option).Error)
	require.JSONEq(t, `{"release-upgrade-model":2.5}`, option.Value)
	var entry Log
	require.NoError(t, LOG_DB.Where("request_id = ?", "release-upgrade-request").First(&entry).Error)
	require.Equal(t, "historical release request", entry.Content)
	require.Equal(t, 123, entry.Quota)
	require.True(t, LOG_DB.Migrator().HasIndex(&AuditLog{}, "idx_audit_time_event"))
	audits, count, err := GetAuditLogs(AuditLogFilter{RequestId: "release-upgrade-audit"}, 0, 10, common.RoleRootUser)
	require.NoError(t, err)
	require.EqualValues(t, 2, count)
	require.Len(t, audits, 2)
	require.Equal(t, []string{"release-upgrade-event-z", "release-upgrade-event-a"}, []string{audits[0].EventId, audits[1].EventId})
	for _, audit := range audits {
		require.Equal(t, user.Id, audit.UserId)
		require.Equal(t, user.Username, audit.Username)
		require.Equal(t, common.RoleAdminUser, audit.ActorRole)
		require.EqualValues(t, 1788600000, audit.CreatedAt)
		require.Equal(t, "historical release audit", audit.Content)
		require.False(t, audit.Success)
		encoded, err := common.Marshal(audit.Other)
		require.NoError(t, err)
		require.JSONEq(t, `{"op":{"action":"model.update","params":{"enabled":false,"limit":0,"large_id":9007199254740993}}}`, string(encoded))
		var numeric struct {
			Op struct {
				Params struct {
					LargeID uint64 `json:"large_id"`
				} `json:"params"`
			} `json:"op"`
		}
		require.NoError(t, common.Unmarshal(encoded, &numeric))
		require.EqualValues(t, 9007199254740993, numeric.Op.Params.LargeID)
	}
	return user, token, subscription, old
}

func host093UniqueIndex(t *testing.T, db *sql.DB, table, column string) string {
	t.Helper()
	var name string
	err := db.QueryRow(`SELECT index_class.relname FROM pg_catalog.pg_index i
JOIN pg_catalog.pg_class table_class ON table_class.oid=i.indrelid
JOIN pg_catalog.pg_namespace n ON n.oid=table_class.relnamespace
JOIN pg_catalog.pg_class index_class ON index_class.oid=i.indexrelid
JOIN pg_catalog.pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=i.indkey[0]
WHERE n.nspname='public' AND table_class.relname=$1 AND a.attname=$2
AND i.indisunique AND i.indisvalid AND i.indisready AND i.indnkeyatts=1
AND i.indpred IS NULL AND i.indexprs IS NULL ORDER BY index_class.relname LIMIT 1`, table, column).Scan(&name)
	require.NoError(t, err, "missing valid unique index: %s.%s", table, column)
	return name
}

// Preserve the donor's actual uniqueness checks without allocating sequences
// or committing rows. Each expected failure owns a separate rollback-only tx.
func host093Uniqueness(t *testing.T, main, log *sql.DB) {
	t.Helper()
	for _, probe := range []struct {
		db            *sql.DB
		table, column string
		query         string
	}{
		{main, "users", "username", `INSERT INTO public.users SELECT (jsonb_populate_record(NULL::public.users, to_jsonb(u) || jsonb_build_object('id',-9000931,'aff_code','host093-unique-name','access_token',NULL))).* FROM public.users u WHERE username='release-upgrade-owner'`},
		{main, "users", "access_token", `INSERT INTO public.users SELECT (jsonb_populate_record(NULL::public.users, to_jsonb(u) || jsonb_build_object('id',-9000931,'username','host093-token','aff_code','host093-unique-token'))).* FROM public.users u WHERE username='release-upgrade-owner'`},
		{log, "audit_logs", "event_id", `INSERT INTO public.audit_logs SELECT (jsonb_populate_record(NULL::public.audit_logs, to_jsonb(a) || jsonb_build_object('id',-9000931))).* FROM public.audit_logs a WHERE event_id='release-upgrade-event-a'`},
	} {
		index := host093UniqueIndex(t, probe.db, probe.table, probe.column)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		tx, err := probe.db.BeginTx(ctx, nil)
		if err != nil {
			cancel()
			t.Fatalf("uniqueness transaction unavailable: %v", err)
		}
		_, insertErr := tx.ExecContext(ctx, probe.query)
		rollbackErr := tx.Rollback()
		cancel()
		require.NoError(t, rollbackErr)
		var pgError *pgconn.PgError
		require.True(t, errors.As(insertErr, &pgError), "expected PostgreSQL uniqueness failure: %s.%s", probe.table, probe.column)
		require.Equal(t, "23505", pgError.Code)
		require.Equal(t, index, pgError.ConstraintName)
	}
}

// Raw PostgreSQL row images include fields omitted by Go JSON tags (credentials,
// auth_version, future nullable columns). Only their SHA256 digests leave RAM.
// Ordered jsonb aggregation also detects missing/extra identical fixture rows.
func host093Snapshots(t *testing.T, main, log *sql.DB) map[string]string {
	t.Helper()
	queries := map[string]string{
		"user": "SELECT * FROM public.users WHERE username='release-upgrade-owner'",
		"token": "SELECT * FROM public.tokens WHERE name='release-upgrade-token'",
		"plan": "SELECT * FROM public.subscription_plans WHERE title='release-upgrade-subscription'",
		"subscription": "SELECT s.* FROM public.user_subscriptions s JOIN public.users u ON u.id=s.user_id WHERE u.username='release-upgrade-owner'",
		"old_receipt": "SELECT * FROM public.subscription_pre_consume_records WHERE request_id='release-upgrade-subscription-request'",
		"fresh_receipt": "SELECT * FROM public.subscription_pre_consume_records WHERE request_id='release-upgrade-new-subscription-request'",
		"mj": "SELECT * FROM public.midjourneys WHERE mj_id='release-upgrade-mj'",
		"metadata": "SELECT * FROM public.models WHERE model_name='release-upgrade-model'",
		"model_ratio": "SELECT * FROM public.options WHERE key='ModelRatio'",
		"deadline": "SELECT * FROM public.options WHERE key='LegacyAccessTokenRetireAt'",
		"ledger": "SELECT l.* FROM public.balance_ledger l JOIN public.users u ON u.id=l.user_id WHERE u.username='release-upgrade-owner'",
		"operations": "SELECT l.* FROM public.balance_ledger_operations l JOIN public.users u ON u.id=l.user_id WHERE u.username='release-upgrade-owner'",
		"finalizations": "SELECT f.* FROM public.billing_finalizations f JOIN public.users u ON u.id=f.user_id WHERE u.username='release-upgrade-owner'",
		"historical_log": "SELECT * FROM public.logs WHERE request_id='release-upgrade-request'",
		"audit": "SELECT * FROM public.audit_logs WHERE request_id='release-upgrade-audit'",
	}
	result := make(map[string]string, len(queries))
	for name, query := range queries {
		db := main
		if name == "historical_log" || name == "audit" {
			db = log
		}
		var rowImage string
		err := db.QueryRow("SELECT COALESCE(jsonb_agg(to_jsonb(q) ORDER BY to_jsonb(q)::text),'[]'::jsonb)::text FROM ("+query+") q").Scan(&rowImage)
		require.NoError(t, err, "full-row snapshot %s", name)
		digest := sha256.Sum256([]byte(rowImage))
		result[name] = hex.EncodeToString(digest[:])
	}
	return result
}

func host093BaselinePath(t *testing.T) string {
	t.Helper()
	path := os.Getenv("TEST_RELEASE_HOST_BASELINE")
	require.True(t, filepath.IsAbs(path), "absolute external baseline path required")
	require.Equal(t, filepath.Clean(path), path)
	parent := filepath.Dir(path)
	resolved, err := filepath.EvalSymlinks(parent)
	require.NoError(t, err)
	require.Equal(t, parent, resolved, "no symlinked baseline parent")
	info, err := os.Stat(parent)
	require.NoError(t, err)
	require.True(t, info.IsDir())
	require.EqualValues(t, 0700, info.Mode().Perm())
	cwd, err := os.Getwd()
	require.NoError(t, err)
	// The supervisor may launch the test ELF from the checkout or model package.
	// Find that checkout without assuming the process cwd is the package path.
	sourceRoot := cwd
	for {
		if info, err := os.Stat(filepath.Join(sourceRoot, "go.mod")); err == nil && info.Mode().IsRegular() {
			break
		}
		parent := filepath.Dir(sourceRoot)
		require.NotEqual(t, sourceRoot, parent, "run verifier from the source checkout")
		sourceRoot = parent
	}
	relative, err := filepath.Rel(sourceRoot, path)
	require.NoError(t, err)
	require.True(t, relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)), "baseline must be outside source")
	return path
}

func host093WriteBaseline(t *testing.T, path string, value host093Baseline) {
	t.Helper()
	encoded, err := json.MarshalIndent(value, "", "  ")
	require.NoError(t, err)
	encoded = append(encoded, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	require.NoError(t, err)
	written, writeErr := file.Write(encoded)
	syncErr := file.Sync()
	closeErr := file.Close()
	require.NoError(t, writeErr)
	require.Equal(t, len(encoded), written)
	require.NoError(t, syncErr)
	require.NoError(t, closeErr)
}

func host093ReadBaseline(t *testing.T, path string) host093Baseline {
	t.Helper()
	info, err := os.Lstat(path)
	require.NoError(t, err)
	require.True(t, info.Mode().IsRegular())
	require.EqualValues(t, 0600, info.Mode().Perm())
	require.Less(t, info.Size(), int64(16384))
	encoded, err := os.ReadFile(path)
	require.NoError(t, err)
	digest := sha256.Sum256(encoded)
	expected := os.Getenv("TEST_RELEASE_HOST_BASELINE_SHA256")
	require.Regexp(t, `^[0-9a-f]{64}$`, expected)
	require.Equal(t, expected, hex.EncodeToString(digest[:]))
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var value host093Baseline
	require.NoError(t, decoder.Decode(&value))
	var trailing any
	require.ErrorIs(t, decoder.Decode(&trailing), io.EOF)
	canonical, err := json.MarshalIndent(value, "", "  ")
	require.NoError(t, err)
	require.True(t, bytes.Equal(encoded, append(canonical, '\n')), "baseline must match the exact typed writer schema")
	require.Equal(t, 1, value.Schema)
	require.Equal(t, "reserved", value.Phase)
	keys := []string{"user", "token", "plan", "subscription", "old_receipt", "fresh_receipt", "mj", "metadata", "model_ratio", "deadline", "ledger", "operations", "finalizations", "historical_log", "audit"}
	require.Len(t, value.Snapshots, len(keys))
	for _, key := range keys {
		digest, ok := value.Snapshots[key]
		require.True(t, ok, "missing full-row snapshot %s", key)
		require.Regexp(t, `^[0-9a-f]{64}$`, digest)
	}
	return value
}
