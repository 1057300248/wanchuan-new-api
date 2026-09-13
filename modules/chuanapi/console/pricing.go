package console

import (
	"maps"
	"net/http"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// pricingVersion identifies the anonymous catalogue projection. Published
// marketing snapshots compare it, so keep it stable across handler moves.
const pricingVersion = "a42d372ccf0b5dd13ecf71203521f9d2"

// GetPublicPricing serves the anonymous marketing catalogue. It always projects
// the public usable-group set and never consults a caller identity, so a stray
// cookie, bearer token, or forged context value cannot reveal private groups or
// personalized discounts.
func GetPublicPricing(c *gin.Context) {
	usableGroup := setting.GetUserUsableGroupsCopy()
	groupRatio := map[string]float64{}
	maps.Copy(groupRatio, ratio_setting.GetGroupRatioCopy())
	for group := range groupRatio {
		if _, ok := usableGroup[group]; !ok {
			delete(groupRatio, group)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success":            true,
		"data":               model.FilterPricingByUsableGroups(model.GetPricing(), usableGroup),
		"vendors":            model.GetVendors(),
		"group_ratio":        groupRatio,
		"usable_group":       usableGroup,
		"supported_endpoint": model.GetSupportedEndpointMap(),
		"auto_groups":        publicAutoGroups(usableGroup),
		"pricing_version":    pricingVersion,
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
