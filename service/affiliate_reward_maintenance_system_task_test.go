package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	affiliatemodule "github.com/QuantumNous/new-api/modules/chuanapi/affiliate"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAffiliateRewardMaintenanceHandlerContract(t *testing.T) {
	handler := affiliatemodule.MaintenanceHandler{}
	assert.Equal(t, model.SystemTaskTypeAffiliateRewardMaintenance, handler.Type())
	assert.True(t, handler.Enabled(), "maturity must continue when new reward creation is disabled")
	assert.Equal(t, time.Minute, handler.Interval())
	assert.Nil(t, handler.NewPayload())
}

func TestAffiliateRewardMaintenanceSystemTaskRecordsSuccessfulRun(t *testing.T) {
	truncate(t)
	seedAffiliateRewardConfig(t, false, 0.2)
	handler := affiliatemodule.MaintenanceHandler{}
	withSystemTaskRegistry(t, handler)

	runSystemTaskScheduler()
	task, err := model.GetLatestSystemTask(model.SystemTaskTypeAffiliateRewardMaintenance)
	require.NoError(t, err)
	require.NotNil(t, task)
	claimed, ok, err := model.ClaimSystemTask(
		task.ID,
		handler.Type(),
		"affiliate-runner",
		common.GetTimestamp()+60,
	)
	require.NoError(t, err)
	require.True(t, ok)

	handler.Run(context.Background(), claimed, "affiliate-runner")

	finished, err := model.GetSystemTaskByTaskID(task.TaskID)
	require.NoError(t, err)
	require.NotNil(t, finished)
	assert.Equal(t, model.SystemTaskStatusSucceeded, finished.Status)
	assert.Empty(t, finished.Error)
	var result affiliatemodule.AffiliateRewardMaintenanceResult
	require.NoError(t, json.Unmarshal([]byte(finished.Result), &result))
	assert.Zero(t, result.MaturedUsers)
	assert.Zero(t, result.MaturedRewards)
}

func TestAffiliateRewardMaintenanceSystemTaskPersistsFailure(t *testing.T) {
	truncate(t)
	seedAffiliateRewardConfig(t, true, 0.2)
	handler := affiliatemodule.MaintenanceHandler{}

	task, err := model.CreateSystemTask(handler.Type(), nil, nil)
	require.NoError(t, err)
	claimed, ok, err := model.ClaimSystemTask(
		task.ID,
		handler.Type(),
		"affiliate-failure-runner",
		common.GetTimestamp()+60,
	)
	require.NoError(t, err)
	require.True(t, ok)

	// The maturity query depends on affiliate_rewards. Removing only that table
	// leaves system_tasks writable, allowing the handler to persist the failure
	// instead of merely emitting a process log.
	require.NoError(t, model.DB.Migrator().DropTable(&model.AffiliateReward{}))
	t.Cleanup(func() {
		require.NoError(t, model.DB.AutoMigrate(&model.AffiliateReward{}))
	})

	handler.Run(context.Background(), claimed, "affiliate-failure-runner")

	finished, err := model.GetSystemTaskByTaskID(task.TaskID)
	require.NoError(t, err)
	require.NotNil(t, finished)
	assert.Equal(t, model.SystemTaskStatusFailed, finished.Status)
	assert.Contains(t, finished.Error, "affiliate reward maturity query failed")
}
