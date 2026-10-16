package console

import (
	"math"
	"net/http"
	"slices"
	"strconv"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// pricingVersion identifies the anonymous catalogue projection. Published
// marketing snapshots compare it, so keep it stable across handler moves.
const pricingVersion = "public-catalog-v2"

// GetPublicPricing serves the anonymous marketing catalogue. It always projects
// the public usable-group set and never consults a caller identity, so a stray
// cookie, bearer token, or forged context value cannot reveal private groups or
// personalized discounts.
func GetPublicPricing(c *gin.Context) {
	usableGroup := setting.GetUserUsableGroupsCopy()
	ratios := ratio_setting.GetGroupRatioCopy()
	aliases := make(map[string]string)
	groupRatio := make(map[string]float64)
	publicGroups := make(map[string]string)
	for group := range usableGroup {
		ratio, ok := ratios[group]
		if !ok || math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 {
			continue
		}
		key := "rate-" + strconv.FormatFloat(ratio, 'f', -1, 64)
		aliases[group] = key
		groupRatio[key] = ratio
		publicGroups[key] = "价格档位 ×" + strconv.FormatFloat(ratio, 'f', -1, 64)
	}
	autoGroups := make([]string, 0)
	for _, group := range publicAutoGroups(usableGroup) {
		if alias, ok := aliases[group]; ok && !slices.Contains(autoGroups, alias) {
			autoGroups = append(autoGroups, alias)
		}
	}
	rows := publicPricingRows(model.FilterPricingByUsableGroups(model.GetPricing(), usableGroup), aliases)
	publicVendorIDs := make(map[int]bool)
	for _, row := range rows {
		publicVendorIDs[row["vendor_id"].(int)] = true
	}
	vendors := make([]gin.H, 0)
	for _, vendor := range model.GetVendors() {
		if !publicVendorIDs[vendor.ID] {
			continue
		}
		vendors = append(vendors, gin.H{"id": vendor.ID, "name": vendor.Name, "icon": vendor.Icon})
	}

	c.JSON(http.StatusOK, gin.H{
		"success":                     true,
		"data":                        rows,
		"vendors":                     vendors,
		"group_ratio":                 groupRatio,
		"usable_group":                publicGroups,
		"supported_endpoint":          model.GetSupportedEndpointMap(),
		"auto_groups":                 autoGroups,
		"pricing_version":             pricingVersion,
		"group_keys_are_display_only": true,
	})
}

// publicAutoGroups mirrors service.GetUserAutoGroup for the anonymous case:
// only groups that are both publicly usable and ratio-configured are
// selectable. It composes the public setting accessors instead of copying the
// user-specific authorization rules that console must not depend on.
func publicAutoGroups(usableGroups map[string]string) []string {
	autoGroups := make([]string, 0)
	seen := make(map[string]struct{})
	for _, group := range setting.GetAutoGroups() {
		if group == "" || group == "auto" {
			continue
		}
		if _, ok := usableGroups[group]; !ok {
			continue
		}
		if !ratio_setting.ContainsGroupRatio(group) {
			continue
		}
		if _, ok := seen[group]; ok {
			continue
		}
		seen[group] = struct{}{}
		autoGroups = append(autoGroups, group)
	}
	return autoGroups
}

// Anonymous catalog fields are an allowlist. Routing identities, operator notes,
// plugin identities and future host fields must not automatically become public.
func publicPricingRows(rows []model.Pricing, aliases map[string]string) []gin.H {
	result := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		if row.ModelName == "stealth/pixel-canary" {
			continue
		}
		groups := make([]string, 0)
		for _, group := range row.EnableGroup {
			if alias, ok := aliases[group]; ok && !slices.Contains(groups, alias) {
				groups = append(groups, alias)
			}
		}
		if len(groups) == 0 {
			continue
		}
		result = append(result, gin.H{
			"model_name": row.ModelName, "vendor_id": row.VendorID,
			"quota_type": row.QuotaType, "model_ratio": row.ModelRatio,
			"model_price": row.ModelPrice, "completion_ratio": row.CompletionRatio,
			"cache_ratio": row.CacheRatio, "create_cache_ratio": row.CreateCacheRatio,
			"image_ratio": row.ImageRatio, "audio_ratio": row.AudioRatio,
			"audio_completion_ratio": row.AudioCompletionRatio,
			"enable_groups":          groups, "supported_endpoint_types": row.SupportedEndpointTypes,
			"billing_mode": row.BillingMode, "billing_expr": row.BillingExpr,
			"billing_variants_present": len(row.BillingPluginVariants) > 0,
			"pricing_version":          row.PricingVersion,
			"context_length":           row.ContextLength, "max_output_tokens": row.MaxOutputTokens,
			"knowledge_cutoff": row.KnowledgeCutoff, "release_date": row.ReleaseDate,
			"parameter_count": row.ParameterCount, "input_modalities": row.InputModalities,
			"output_modalities": row.OutputModalities, "capabilities": row.Capabilities,
			"bonus_eligible": row.BonusEligible,
		})
	}
	return result
}
