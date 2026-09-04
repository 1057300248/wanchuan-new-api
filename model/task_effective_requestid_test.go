package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskPrivateData_EffectiveRequestID(t *testing.T) {
	tests := []struct {
		name     string
		data     *TaskPrivateData
		expected string
	}{
		{
			name:     "nil receiver",
			data:     nil,
			expected: "",
		},
		{
			name:     "both empty",
			data:     &TaskPrivateData{},
			expected: "",
		},
		{
			name: "execution only",
			data: &TaskPrivateData{
				Execution: &TaskExecutionSnapshot{
					RequestID: "req-exec-001",
				},
			},
			expected: "req-exec-001",
		},
		{
			name: "legacy only",
			data: &TaskPrivateData{
				RequestId: "req-legacy-002",
			},
			expected: "req-legacy-002",
		},
		{
			name: "both equal",
			data: &TaskPrivateData{
				Execution: &TaskExecutionSnapshot{
					RequestID: "req-same-003",
				},
				RequestId: "req-same-003",
			},
			expected: "req-same-003",
		},
		{
			name: "both conflict - execution wins",
			data: &TaskPrivateData{
				Execution: &TaskExecutionSnapshot{
					RequestID: "req-exec-004",
				},
				RequestId: "req-legacy-conflict",
			},
			expected: "req-exec-004",
		},
		{
			name: "execution present but empty string - fallback to legacy",
			data: &TaskPrivateData{
				Execution: &TaskExecutionSnapshot{
					RequestID: "",
				},
				RequestId: "req-legacy-005",
			},
			expected: "req-legacy-005",
		},
		{
			name: "execution nil but legacy present",
			data: &TaskPrivateData{
				Execution: nil,
				RequestId: "req-legacy-006",
			},
			expected: "req-legacy-006",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.data.EffectiveRequestID()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFindTaskByRequestId_ExecutionPrecedence(t *testing.T) {
	truncateTables(t)

	// 创建测试用户。
	user := &User{
		Username:    "test-req-id-user-" + common.GetRandomString(6),
		Password:    "unused-password-hash",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
		AffCode:     "test-req-id-aff-" + common.GetRandomString(8),
	}
	err := DB.Create(user).Error
	require.NoError(t, err)

	// 场景 1: legacy-only 任务可被查到
	taskLegacy := &Task{
		UserId:   user.Id,
		TaskID:   GenerateTaskID(),
		Platform: constant.TaskPlatformSuno,
		Action:   "test",
		Status:   TaskStatusSuccess,
		PrivateData: TaskPrivateData{
			RequestId: "req-legacy-find-001",
		},
	}
	err = DB.Create(taskLegacy).Error
	require.NoError(t, err)

	found, ok, err := FindTaskByRequestId(user.Id, "req-legacy-find-001")
	require.NoError(t, err)
	require.True(t, ok, "legacy-only 任务应当被找到")
	assert.Equal(t, taskLegacy.TaskID, found.TaskID)
	assert.Equal(t, "req-legacy-find-001", found.PrivateData.EffectiveRequestID())

	// 场景 2: execution-only 任务可被查到
	taskExec := &Task{
		UserId:   user.Id,
		TaskID:   GenerateTaskID(),
		Platform: constant.TaskPlatformSuno,
		Action:   "test",
		Status:   TaskStatusSuccess,
		PrivateData: TaskPrivateData{
			Execution: &TaskExecutionSnapshot{
				RequestID: "req-exec-find-002",
			},
		},
	}
	err = DB.Create(taskExec).Error
	require.NoError(t, err)

	found, ok, err = FindTaskByRequestId(user.Id, "req-exec-find-002")
	require.NoError(t, err)
	require.True(t, ok, "execution-only 任务应当被找到")
	assert.Equal(t, taskExec.TaskID, found.TaskID)
	assert.Equal(t, "req-exec-find-002", found.PrivateData.EffectiveRequestID())

	// 场景 3: conflict 时不得用 legacy 值错误命中
	taskConflict := &Task{
		UserId:   user.Id,
		TaskID:   GenerateTaskID(),
		Platform: constant.TaskPlatformSuno,
		Action:   "test",
		Status:   TaskStatusSuccess,
		PrivateData: TaskPrivateData{
			Execution: &TaskExecutionSnapshot{
				RequestID: "req-exec-find-003",
			},
			RequestId: "req-legacy-conflict-003",
		},
	}
	err = DB.Create(taskConflict).Error
	require.NoError(t, err)

	// 用 Execution.RequestID 查询应当命中
	found, ok, err = FindTaskByRequestId(user.Id, "req-exec-find-003")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, taskConflict.TaskID, found.TaskID)

	// 用 legacy RequestId 查询不得命中（因为 EffectiveRequestID 返回 Execution）
	found, ok, err = FindTaskByRequestId(user.Id, "req-legacy-conflict-003")
	require.NoError(t, err)
	assert.False(t, ok, "conflict 时用 legacy 值查询不应命中")
}

func TestAffiliateRewardCostRefsUsesEffectiveRequestID(t *testing.T) {
	truncateTables(t)

	user := &User{
		Username:    "test-aff-ref-user-" + common.GetRandomString(6),
		Password:    "unused-password-hash",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
		AffCode:     "test-aff-ref-" + common.GetRandomString(8),
	}
	require.NoError(t, DB.Create(user).Error)

	task := &Task{
		UserId:   user.Id,
		TaskID:   GenerateTaskID(),
		Platform: constant.TaskPlatformSuno,
		Action:   "test",
		Status:   TaskStatusSuccess,
		PrivateData: TaskPrivateData{
			Execution: &TaskExecutionSnapshot{RequestID: "req-exec-aff-001"},
			RequestId: "req-legacy-aff-conflict",
		},
	}
	require.NoError(t, DB.Create(task).Error)

	refs, ready, err := affiliateRewardCostRefsTx(DB, &AffiliateReward{
		SourceUserId:  user.Id,
		ReferenceType: "task",
		ReferenceId:   task.TaskID,
	})
	require.NoError(t, err)
	require.True(t, ready)
	assert.Equal(t, [][2]string{
		{"relay", "req-exec-aff-001"},
		{"task", task.TaskID},
	}, refs)
}
