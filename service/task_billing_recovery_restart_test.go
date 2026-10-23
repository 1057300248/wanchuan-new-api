package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const recoveryRestartFileEnv = "NEW_API_TEST_RECOVERY_RESTART_FILE"
const recoveryRestartPhaseEnv = "NEW_API_TEST_RECOVERY_RESTART_PHASE"

func migrateRecoveryFixture(t *testing.T, db *gorm.DB) {
	t.Helper()
	tables, err := db.Migrator().GetTables()
	require.NoError(t, err)
	require.Empty(t, tables, "refusing a nonempty disposable recovery database")
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.User{}, &model.Token{}, &model.Channel{}, &model.Log{}, &model.BillingFinalization{}, &model.SystemTask{}, &model.SystemTaskLock{}, &model.Midjourney{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{}, &model.UserSubscription{}, &model.SubscriptionPreConsumeRecord{}))
}

func bindRecoveryFixture(t *testing.T, db *gorm.DB, dialect common.DatabaseType) {
	t.Helper()
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
	})
}

// Two distinct, joined OS processes run successive real jobs against one file.
// This proves restart persistence, not native SQL row-lock behavior or a crash.
func TestBillingRecoveryFileRestart(t *testing.T) {
	if phase := os.Getenv(recoveryRestartPhaseEnv); phase != "" {
		t.Cleanup(func() {
			if !t.Failed() && !t.Skipped() {
				fmt.Println(refundFixtureChecked + t.Name())
			}
		}) // Runs after the child's binding restoration and SQL-pool close.
		require.Contains(t, []string{"history", "resume"}, phase)
		path := os.Getenv(recoveryRestartFileEnv)
		require.NotEmpty(t, path)
		_, err := os.Stat(path)
		require.NoError(t, err, "the child must open the parent's existing database")
		db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
		require.NoError(t, err)
		pool, err := db.DB()
		require.NoError(t, err)
		pool.SetMaxOpenConns(1)
		t.Cleanup(func() { require.NoError(t, pool.Close()) })
		bindRecoveryFixture(t, db, common.DatabaseTypeSQLite)
		handler := billingRefundRecoveryHandler{}
		require.True(t, handler.Enabled())
		job, err := model.CreateSystemTask(handler.Type(), nil, nil)
		require.NoError(t, err)
		runner := fmt.Sprintf("restart-process-%d", os.Getpid())
		claimed, acquired, err := model.ClaimSystemTask(job.ID, handler.Type(), runner, time.Now().Add(time.Minute).Unix())
		require.NoError(t, err)
		require.True(t, acquired)
		handler.Run(t.Context(), claimed, runner)
		finished, err := model.GetSystemTaskByTaskID(job.TaskID)
		require.NoError(t, err)
		require.NotNil(t, finished)
		require.Equal(t, model.SystemTaskStatusSucceeded, finished.Status)
		require.Positive(t, recoveryStoredCursor(t, finished).TaskID)
		// No job/actor cleanup: the next process must consume this persisted state.
		t.Logf("recovery-restart-complete:%s pid=%d", phase, os.Getpid())
		return
	}

	path := filepath.Join(t.TempDir(), "recovery.sqlite")
	open := func() *gorm.DB {
		db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
		require.NoError(t, err)
		pool, err := db.DB()
		require.NoError(t, err)
		pool.SetMaxOpenConns(1)
		return db
	}
	db := open()
	t.Cleanup(func() {
		pool, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, pool.Close())
	})
	migrateRecoveryFixture(t, db)
	bindRecoveryFixture(t, db, common.DatabaseTypeSQLite)
	legacy, oldUser, oldToken, oldChannel := seedRecoveryLegacyPage(t, 101)
	oldMoney := reviewAccounting(t, oldUser, oldToken, oldChannel)
	payable, user, token, channel := seedAtomicFinancialTask(t, true)
	before := reviewAccounting(t, user, token, channel)
	executable, err := os.Executable()
	require.NoError(t, err)
	var firstID int64
	for _, phase := range []string{"history", "resume"} {
		pool, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, pool.Close(), "no parent SQL connection may carry state across the process boundary")
		budget := 30 * time.Second
		if deadline, ok := t.Deadline(); ok {
			budget = min(budget, time.Until(deadline)-3*time.Second)
		}
		require.Greater(t, budget, 3*time.Second)
		ctx, cancel := context.WithTimeout(t.Context(), budget)
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestBillingRecoveryFileRestart$", "-test.count=1", "-test.v=true", "-test.timeout="+(budget-time.Second).String())
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, recoveryRestartFileEnv+"=") && !strings.HasPrefix(entry, recoveryRestartPhaseEnv+"=") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, recoveryRestartFileEnv+"="+path, recoveryRestartPhaseEnv+"="+phase)
		cmd.WaitDelay = time.Second
		output, runErr := cmd.CombinedOutput() // joined even on timeout; no detached worker
		ctxErr := ctx.Err()
		cancel()
		// Reopen before assertions so owned actor cleanup also works on failure.
		db = open()
		model.DB, model.LOG_DB = db, db
		t.Logf("restart %s child output:\n%s", phase, output)
		require.NoError(t, validateRefundFixtureChild(t.Name(), []string{t.Name()}, output, runErr, ctxErr))
		require.Contains(t, string(output), "recovery-restart-complete:"+phase)
		latest, err := model.GetLatestSystemTask((billingRefundRecoveryHandler{}).Type())
		require.NoError(t, err)
		require.NotNil(t, latest)
		if phase == "history" {
			firstID = latest.ID
			require.Equal(t, legacy[99].ID, recoveryStoredCursor(t, latest).TaskID)
			require.Equal(t, before, reviewAccounting(t, user, token, channel))
		} else {
			require.Greater(t, latest.ID, firstID)
			require.Equal(t, payable.ID, recoveryStoredCursor(t, latest).TaskID)
			requireRecoveryRefund(t, payable, user, token, channel)
		}
	}
	after := reviewAccounting(t, user, token, channel)
	recoveryFaultPass(t, model.SystemTaskStatusSucceeded)
	require.Equal(t, after, reviewAccounting(t, user, token, channel))
	for _, original := range legacy {
		var stored model.Task
		require.NoError(t, model.DB.First(&stored, original.ID).Error)
		assert.Equal(t, original, stored)
	}
	assert.Equal(t, oldMoney, reviewAccounting(t, oldUser, oldToken, oldChannel))
}

// Dedicated empty native databases are required. Distinct connections exercise
// serial lease handoff and a financial replay; this is not a two-process worker
// race, and SQLite is intentionally not an alternative for this test.
func TestBillingRecoveryNativeSessionLeaseHandoff(t *testing.T) {
	for _, dialect := range []struct {
		name common.DatabaseType
		env  string
	}{
		{common.DatabaseTypeMySQL, "TEST_BILLING_RECOVERY_MYSQL_DSN"},
		{common.DatabaseTypePostgreSQL, "TEST_BILLING_RECOVERY_POSTGRES_DSN"},
	} {
		t.Run(string(dialect.name), func(t *testing.T) {
			dsn := os.Getenv(dialect.env)
			if dsn == "" {
				t.Skip(dialect.env + " not configured")
			}
			open := func() *gorm.DB {
				var driver gorm.Dialector = mysql.Open(dsn)
				if dialect.name == common.DatabaseTypePostgreSQL {
					driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
				}
				db, err := gorm.Open(driver, &gorm.Config{})
				require.NoError(t, err)
				pool, err := db.DB()
				require.NoError(t, err)
				pool.SetMaxOpenConns(1)
				pool.SetMaxIdleConns(1)
				t.Cleanup(func() { require.NoError(t, pool.Close()) })
				return db
			}
			a, b := open(), open()
			migrateRecoveryFixture(t, a)
			bindRecoveryFixture(t, a, dialect.name)
			query := "SELECT CONNECTION_ID()"
			if dialect.name == common.DatabaseTypePostgreSQL {
				query = "SELECT pg_backend_pid()"
			}
			var aID, bID int64
			require.NoError(t, a.Raw(query).Scan(&aID).Error)
			require.NoError(t, b.Raw(query).Scan(&bID).Error)
			require.NotEqual(t, aID, bID, "the handoff must cross actual server sessions, not GORM structs")
			var version string
			require.NoError(t, a.Raw("SELECT version()").Scan(&version).Error)
			t.Logf("native recovery handoff: %s %s; distinct server sessions %d/%d", dialect.name, version, aID, bID)

			legacy, oldUser, oldToken, oldChannel := seedRecoveryLegacyPage(t, 101)
			oldMoney := reviewAccounting(t, oldUser, oldToken, oldChannel)
			payable, user, token, channel := seedAtomicFinancialTask(t, true)
			before := reviewAccounting(t, user, token, channel)
			handler := billingRefundRecoveryHandler{}
			first, err := model.CreateSystemTask(handler.Type(), nil, nil)
			require.NoError(t, err)
			ownedJobIDs := []string{first.TaskID}
			t.Cleanup(func() {
				require.NoError(t, a.Where("task_id IN ?", ownedJobIDs).Delete(&model.SystemTaskLock{}).Error)
				require.NoError(t, a.Where("task_id IN ?", ownedJobIDs).Delete(&model.SystemTask{}).Error)
			})
			_, acquired, err := model.ClaimSystemTask(first.ID, handler.Type(), "native-recovery-a", time.Now().Add(time.Minute).Unix())
			require.NoError(t, err)
			require.True(t, acquired)
			require.NoError(t, model.UpdateSystemTaskState(first.TaskID, "native-recovery-a", billingRecoveryCursor{TaskID: legacy[99].ID}))
			// An old pending row may predate the active-key invariant. It must
			// still be blocked by the live type lease on the other connection.
			second := &model.SystemTask{TaskID: "native-recovery-pending", Type: handler.Type(), Status: model.SystemTaskStatusPending}
			require.NoError(t, a.Create(second).Error)
			ownedJobIDs = append(ownedJobIDs, second.TaskID)
			model.DB, model.LOG_DB = b, b
			_, acquired, err = model.ClaimSystemTask(second.ID, handler.Type(), "native-recovery-b", time.Now().Add(time.Minute).Unix())
			require.NoError(t, err)
			require.False(t, acquired)
			require.Equal(t, before, reviewAccounting(t, user, token, channel))
			require.NoError(t, a.Model(&model.SystemTaskLock{}).Where("task_id = ? AND locked_by = ?", first.TaskID, "native-recovery-a").Update("locked_until", common.GetTimestamp()-1).Error)
			claimed, acquired, err := model.ClaimSystemTask(second.ID, handler.Type(), "native-recovery-b", time.Now().Add(time.Minute).Unix())
			require.NoError(t, err)
			require.True(t, acquired)
			model.DB, model.LOG_DB = a, a
			require.ErrorIs(t, model.UpdateSystemTaskState(first.TaskID, "native-recovery-a", billingRecoveryCursor{TaskID: payable.ID}), model.ErrSystemTaskLockLost)
			require.ErrorIs(t, model.FinishSystemTask(first.TaskID, "native-recovery-a", model.SystemTaskStatusSucceeded, nil, ""), model.ErrSystemTaskLockLost)
			var expired model.SystemTask
			require.NoError(t, a.First(&expired, first.ID).Error)
			require.Equal(t, model.SystemTaskStatusFailed, expired.Status)
			require.Equal(t, "task lease expired", expired.Error)
			require.Equal(t, legacy[99].ID, recoveryStoredCursor(t, &expired).TaskID)
			model.DB, model.LOG_DB = b, b
			handler.Run(t.Context(), claimed, "native-recovery-b")
			finished, err := model.GetSystemTaskByTaskID(second.TaskID)
			require.NoError(t, err)
			require.NotNil(t, finished)
			require.Equal(t, model.SystemTaskStatusSucceeded, finished.Status)
			require.Equal(t, payable.ID, recoveryStoredCursor(t, finished).TaskID)
			after := requireRecoveryRefund(t, payable, user, token, channel)
			// The independent immediate financial executor has no system-task
			// lease; its idempotency comes from the durable task receipt instead.
			model.DB, model.LOG_DB = a, a
			applied, err := ApplyTaskQuota(t.Context(), payable, 0, true)
			require.NoError(t, err)
			require.False(t, applied)
			require.Equal(t, after, reviewAccounting(t, user, token, channel))
			for _, original := range legacy {
				var stored model.Task
				require.NoError(t, a.First(&stored, original.ID).Error)
				assert.Equal(t, original, stored)
			}
			assert.Equal(t, oldMoney, reviewAccounting(t, oldUser, oldToken, oldChannel))
		})
	}
}
