package model

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

const (
	NameRuleExact = iota
	NameRulePrefix
	NameRuleContains
	NameRuleSuffix
)

type BoundChannel struct {
	Name string `json:"name"`
	Type int    `json:"type"`
}

type Model struct {
	Id           int            `json:"id"`
	ModelName    string         `json:"model_name" gorm:"size:128;not null;uniqueIndex:uk_model_name_delete_at,priority:1"`
	Description  string         `json:"description,omitempty" gorm:"type:text"`
	Icon         string         `json:"icon,omitempty" gorm:"type:varchar(128)"`
	Tags         string         `json:"tags,omitempty" gorm:"type:varchar(255)"`
	VendorID     int            `json:"vendor_id,omitempty" gorm:"index"`
	Endpoints    string         `json:"endpoints,omitempty" gorm:"type:text"`
	Status       int            `json:"status" gorm:"default:1"`
	SyncOfficial int            `json:"sync_official" gorm:"default:1"`
	CreatedTime  int64          `json:"created_time" gorm:"bigint"`
	UpdatedTime  int64          `json:"updated_time" gorm:"bigint"`
	DeletedAt    gorm.DeletedAt `json:"-" gorm:"index;uniqueIndex:uk_model_name_delete_at,priority:2"`

	// 模型元数据（目录字段，未知值保持 NULL/空，不由模型名推断）。
	// 数组字段以 JSON 数组字符串形式存储（与 endpoints 一致），读取侧负责解析与降级。
	ContextLength    *int   `json:"context_length,omitempty" gorm:"bigint"`
	MaxOutputTokens  *int   `json:"max_output_tokens,omitempty" gorm:"bigint"`
	KnowledgeCutoff  string `json:"knowledge_cutoff,omitempty" gorm:"type:varchar(64)"`
	ReleaseDate      string `json:"release_date,omitempty" gorm:"type:varchar(64)"`
	ParameterCount   string `json:"parameter_count,omitempty" gorm:"type:varchar(64)"`
	InputModalities  string `json:"input_modalities,omitempty" gorm:"type:text"`
	OutputModalities string `json:"output_modalities,omitempty" gorm:"type:text"`
	Capabilities     string `json:"capabilities,omitempty" gorm:"type:text"`
	// BonusEligible 三态：nil=尚未配置，false=不可用，true=可用。不使用 GORM default 标签。
	BonusEligible *bool `json:"bonus_eligible,omitempty"`

	BoundChannels []BoundChannel `json:"bound_channels,omitempty" gorm:"-"`
	EnableGroups  []string       `json:"enable_groups,omitempty" gorm:"-"`
	QuotaTypes    []int          `json:"quota_types,omitempty" gorm:"-"`
	NameRule      int            `json:"name_rule" gorm:"default:0"`

	MatchedModels []string `json:"matched_models,omitempty" gorm:"-"`
	MatchedCount  int      `json:"matched_count,omitempty" gorm:"-"`
}

func (mi *Model) Insert() error {
	now := common.GetTimestamp()
	mi.CreatedTime = now
	mi.UpdatedTime = now

	// 保存原始值（因为 Create 后可能被 GORM 的 default 标签覆盖为 1）
	originalStatus := mi.Status
	originalSyncOfficial := mi.SyncOfficial

	// 先创建记录（GORM 会对零值字段应用默认值）
	if err := DB.Create(mi).Error; err != nil {
		return err
	}

	// 使用保存的原始值进行更新，确保零值能正确保存
	return DB.Model(&Model{}).Where("id = ?", mi.Id).Updates(map[string]interface{}{
		"status":        originalStatus,
		"sync_official": originalSyncOfficial,
	}).Error
}

func IsModelNameDuplicated(id int, name string) (bool, error) {
	if name == "" {
		return false, nil
	}
	var cnt int64
	err := DB.Model(&Model{}).Where("model_name = ? AND id <> ?", name, id).Count(&cnt).Error
	return cnt > 0, err
}

func (mi *Model) Update() error {
	mi.UpdatedTime = common.GetTimestamp()
	// 使用 Select 强制更新所有字段，包括零值
	return DB.Model(&Model{}).Where("id = ?", mi.Id).
		Select("model_name", "description", "icon", "tags", "vendor_id", "endpoints", "status", "sync_official", "name_rule", "updated_time",
			"context_length", "max_output_tokens", "knowledge_cutoff", "release_date", "parameter_count",
			"input_modalities", "output_modalities", "capabilities", "bonus_eligible").
		Updates(mi).Error
}

// ParseModelMetadataArray 将 models 表中的 JSON 数组字符串解析为字符串切片。
// 空值、null 或无效存量值安全降级为空数组，保证 /api/pricing 始终输出 JSON 数组。
func ParseModelMetadataArray(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{}
	}
	var items []string
	if err := common.Unmarshal([]byte(raw), &items); err != nil {
		return []string{}
	}
	if items == nil {
		return []string{}
	}
	return items
}

func (mi *Model) Delete() error {
	return DB.Delete(mi).Error
}

func GetVendorModelCounts() (map[int64]int64, error) {
	var stats []struct {
		VendorID int64
		Count    int64
	}
	if err := DB.Model(&Model{}).
		Select("vendor_id as vendor_id, count(*) as count").
		Group("vendor_id").
		Scan(&stats).Error; err != nil {
		return nil, err
	}
	m := make(map[int64]int64, len(stats))
	for _, s := range stats {
		m[s.VendorID] = s.Count
	}
	return m, nil
}

func GetAllModels(offset int, limit int) ([]*Model, error) {
	models, _, err := SearchModels("", "", "", "", offset, limit)
	return models, err
}

func GetBoundChannelsByModelsMap(modelNames []string) (map[string][]BoundChannel, error) {
	result := make(map[string][]BoundChannel)
	if len(modelNames) == 0 {
		return result, nil
	}
	type row struct {
		Model string
		Name  string
		Type  int
	}
	var rows []row
	err := DB.Table("channels").
		Select("abilities.model as model, channels.name as name, channels.type as type").
		Joins("JOIN abilities ON abilities.channel_id = channels.id").
		Where("abilities.model IN ? AND abilities.enabled = ?", modelNames, true).
		Distinct().
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		result[r.Model] = append(result[r.Model], BoundChannel{Name: r.Name, Type: r.Type})
	}
	return result, nil
}

func normalizeLookupValues(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	return normalized
}

func GetPreferredModelOwnerChannelTypes(modelNames []string, groups []string) (map[string]int, error) {
	result := make(map[string]int)
	modelNames = normalizeLookupValues(modelNames)
	if len(modelNames) == 0 {
		return result, nil
	}

	type row struct {
		Model       string
		ChannelType int
	}
	var rows []row

	query := DB.Table("abilities").
		Select("abilities.model as model, channels.type as channel_type").
		Joins("JOIN channels ON abilities.channel_id = channels.id").
		Where("abilities.model IN ? AND abilities.enabled = ? AND channels.status = ?", modelNames, true, common.ChannelStatusEnabled).
		Order("COALESCE(abilities.priority, 0) DESC").
		Order("abilities.weight DESC").
		Order("abilities.channel_id ASC")

	groups = normalizeLookupValues(groups)
	if len(groups) > 0 {
		query = query.Where("abilities."+commonGroupCol+" IN ?", groups)
	}

	if err := query.Scan(&rows).Error; err != nil {
		return nil, err
	}

	for _, r := range rows {
		if _, ok := result[r.Model]; ok {
			continue
		}
		result[r.Model] = r.ChannelType
	}
	return result, nil
}

func SearchModels(keyword string, vendor string, status string, syncOfficial string, offset int, limit int) ([]*Model, int64, error) {
	var models []*Model
	db := DB.Model(&Model{})
	if keyword != "" {
		like := "%" + keyword + "%"
		db = db.Where("model_name LIKE ? OR description LIKE ? OR tags LIKE ?", like, like, like)
	}
	if vendor != "" {
		if vid, err := strconv.Atoi(vendor); err == nil {
			db = db.Where("models.vendor_id = ?", vid)
		} else {
			db = db.Joins("JOIN vendors ON vendors.id = models.vendor_id").Where("vendors.name LIKE ?", "%"+vendor+"%")
		}
	}
	if statusValue, ok := parseModelStatusFilter(status); ok {
		db = db.Where("models.status = ?", statusValue)
	}
	if syncValue, ok := parseModelSyncFilter(syncOfficial); ok {
		db = db.Where("models.sync_official = ?", syncValue)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := db.Order("models.id DESC").Offset(offset).Limit(limit).Find(&models).Error; err != nil {
		return nil, 0, err
	}
	return models, total, nil
}

// parseModelStatusFilter maps UI/API status values to the models.status column.
// Returns ok=false when no status filter should be applied.
func parseModelStatusFilter(status string) (value int, ok bool) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "all":
		return 0, false
	case "enabled", "1":
		return 1, true
	case "disabled", "0":
		return 0, true
	default:
		n, err := strconv.Atoi(status)
		if err != nil {
			return 0, false
		}
		return n, true
	}
}

// parseModelSyncFilter maps UI/API sync values to the models.sync_official column.
// Returns ok=false when no sync filter should be applied.
func parseModelSyncFilter(syncOfficial string) (value int, ok bool) {
	switch strings.ToLower(strings.TrimSpace(syncOfficial)) {
	case "", "all":
		return 0, false
	case "yes", "1":
		return 1, true
	case "no", "0":
		return 0, true
	default:
		n, err := strconv.Atoi(syncOfficial)
		if err != nil {
			return 0, false
		}
		return n, true
	}
}
