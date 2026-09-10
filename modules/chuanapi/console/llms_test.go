package console

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderLlmsFullIncludesPublicSectionsAndExcludesInternalPricingFields(t *testing.T) {
	bonusEligible := true
	models := publicModelsFromPricing([]model.Pricing{
		{
			ModelName:              "fixture-model",
			Description:            "Public model description",
			QuotaType:              0,
			ModelRatio:             1.25,
			CompletionRatio:        2,
			EnableGroup:            []string{"default"},
			SupportedEndpointTypes: []constant.EndpointType{constant.EndpointTypeOpenAI},
			InputModalities:        []string{"text"},
			OutputModalities:       []string{"text"},
			Capabilities:           []string{"streaming"},
			BonusEligible:          &bonusEligible,
			BillingMode:            "tiered_expr",
			BillingExpr:            "SECRET_BILLING_EXPRESSION",
			PricingVersion:         "INTERNAL_PRICING_VERSION",
		},
	})
	require.Len(t, models, 1)

	output := renderLlmsFull(llmsPageData{
		SystemName: "Fixture API",
		Version:    "v1.2.3",
		BaseURL:    "https://fixture.example",
		Notice:     "Maintenance window",
		Announcements: []llmsAnnouncement{
			{Content: "New docs are live", PublishDate: "2026-08-31T00:00:00Z"},
		},
		FAQ: []llmsFAQ{
			{Question: "Where are the docs?", Answer: "Use /help."},
		},
		Models: models,
	})

	assert.Contains(t, output, "## Site information")
	assert.Contains(t, output, "## API endpoints")
	assert.Contains(t, output, "## Models and pricing metadata")
	assert.Contains(t, output, "| POST | `/v1/chat/completions` |")
	assert.Contains(t, output, "fixture-model")
	assert.Contains(t, output, "tiered usage pricing")
	assert.Contains(t, output, "Use /help.\n\n## API endpoints")
	assert.NotContains(t, output, "SECRET_BILLING_EXPRESSION")
	assert.NotContains(t, output, "INTERNAL_PRICING_VERSION")
}

func TestRenderLlmsIndexLinksHumanAndFullDocumentation(t *testing.T) {
	output := renderLlmsIndex(llmsPageData{
		SystemName: "Fixture API",
		Version:    "v1.2.3",
		BaseURL:    "https://fixture.example",
	})

	assert.Contains(t, output, "https://fixture.example/help")
	assert.Contains(t, output, "https://fixture.example/help/api")
	assert.Contains(t, output, "https://fixture.example/llms-full.txt")
	assert.Contains(t, output, "POST /v1/responses")
}
