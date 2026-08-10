package model

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// 模型赠金资格批量调整与独立审计（TokenMix P0-5）
//
// 批量调整写入独立审计表 model_bonus_eligible_audits，记录操作者、模型、
// 变更前后值、请求幂等键与时间。审计行以 (请求幂等键 + 模型) 唯一约束保证
// 重复请求幂等：同键同目标值重放无副作用；同键不同目标值视为冲突拒绝。
// ---------------------------------------------------------------------------

// ModelBonusEligibleAudit 模型 bonus_eligible 变更的独立审计记录。
type ModelBonusEligibleAudit struct {
	Id             int    `json:"id" gorm:"primaryKey;autoIncrement"`
	OperatorId     int    `json:"operator_id" gorm:"not null;index"`
	OperatorName   string `json:"operator_name" gorm:"type:varchar(64)"`
	ModelId        int    `json:"model_id" gorm:"not null;index"`
	ModelName      string `json:"model_name" gorm:"type:varchar(128);not null;index"`
	Before         *bool  `json:"before"`
	After          *bool  `json:"after"`
	IdempotencyKey string `json:"idempotency_key" gorm:"type:varchar(255);not null;uniqueIndex"`
	CreatedAt      int64  `json:"created_at" gorm:"bigint;autoCreateTime;index"`
}

func (ModelBonusEligibleAudit) TableName() string {
	return "model_bonus_eligible_audits"
}

var (
	ErrModelAuditConflict       = errors.New("model bonus eligibility request conflicts with an earlier request with the same idempotency key")
	ErrModelBonusEligibleNoRows = errors.New("no model matched the request")
)

// BatchUpdateModelBonusEligible 批量调整模型 bonus_eligible 并写独立审计。
// target 为 nil 表示清除资格（回到未配置状态）。
// idempotencyKey 由调用方提供（前端 crypto.randomUUID 或后端生成），
// 同一请求重复提交不重复变更；同键但目标值不同的请求返回冲突错误。
// 返回实际变更（before != after）的模型数。
func BatchUpdateModelBonusEligible(modelIDs []int, target *bool, operatorId int, operatorName string, idempotencyKey string) (int, error) {
	if len(modelIDs) == 0 {
		return 0, ErrModelBonusEligibleNoRows
	}
	if idempotencyKey == "" {
		return 0, fmt.Errorf("idempotency key is required")
	}
	modelIDs = dedupeInts(modelIDs)

	var changed int
	err := DB.Transaction(func(tx *gorm.DB) error {
		for _, modelID := range modelIDs {
			auditKey := idempotencyKey + ":" + fmt.Sprintf("%d", modelID)
			// 先核对幂等操作参数，再查看当前模型状态。这样即使模型后来被其他
			// 请求改回目标值，同一幂等键携带不同 target 仍会明确返回冲突。
			var existing ModelBonusEligibleAudit
			findErr := tx.Where("idempotency_key = ?", auditKey).Limit(1).Find(&existing).Error
			if findErr != nil {
				return findErr
			}
			if existing.Id > 0 {
				if boolPtrEqual(existing.After, target) {
					continue
				}
				return ErrModelAuditConflict
			}

			var model Model
			if err := tx.Select("id", "model_name", "bonus_eligible").Where("id = ?", modelID).First(&model).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				return err
			}
			if boolPtrEqual(model.BonusEligible, target) {
				continue
			}

			var targetValue interface{}
			if target != nil {
				targetValue = *target
			}
			if err := tx.Model(&Model{}).Where("id = ?", modelID).Update("bonus_eligible", targetValue).Error; err != nil {
				return err
			}
			audit := ModelBonusEligibleAudit{
				OperatorId:     operatorId,
				OperatorName:   operatorName,
				ModelId:        model.Id,
				ModelName:      model.ModelName,
				Before:         model.BonusEligible,
				After:          target,
				IdempotencyKey: auditKey,
				CreatedAt:      time.Now().Unix(),
			}
			if err := tx.Create(&audit).Error; err != nil {
				if isDuplicateKeyError(err) {
					continue
				}
				return err
			}
			changed++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	InvalidateModelBonusEligibleCache()
	return changed, nil
}

func dedupeInts(values []int) []int {
	seen := make(map[int]struct{}, len(values))
	out := make([]int, 0, len(values))
	for _, v := range values {
		if v <= 0 {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func boolPtrEqual(a, b *bool) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// SearchModelBonusEligibleAudits 查询赠金资格调整审计记录（后端审计查询）。
func SearchModelBonusEligibleAudits(modelID int, operatorID int, keyword string, startIdx int, pageSize int) ([]*ModelBonusEligibleAudit, int64, error) {
	db := DB.Model(&ModelBonusEligibleAudit{})
	if modelID > 0 {
		db = db.Where("model_id = ?", modelID)
	}
	if operatorID > 0 {
		db = db.Where("operator_id = ?", operatorID)
	}
	if keyword != "" {
		like := "%" + keyword + "%"
		db = db.Where("model_name LIKE ? OR operator_name LIKE ? OR idempotency_key LIKE ?", like, like, like)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var audits []*ModelBonusEligibleAudit
	if err := db.Order("id desc").Offset(startIdx).Limit(pageSize).Find(&audits).Error; err != nil {
		return nil, 0, err
	}
	return audits, total, nil
}
