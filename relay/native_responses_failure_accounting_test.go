package relay_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	walletmodule "github.com/QuantumNous/new-api/modules/chuanapi/wallet"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"github.com/QuantumNous/new-api/pkg/walletport"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The native HTTP handler and WebSocket event accumulator share settlement.
// Exercise that boundary with real reservations, wallet/token transactions and
// a separate consume-log database, including successful missing-usage controls.
func TestNativeResponsesFailureFixedAccountingDatabaseMatrix(t *testing.T) {
	oldRedis, oldMemory, oldBatch := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled
	oldConsume, oldCount, oldTimeout := common.LogConsumeEnabled, constant.CountToken, constant.StreamingTimeout
	oldQuota, oldMode := common.QuotaPerUnit, gin.Mode()
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = false, false, false
	common.LogConsumeEnabled, constant.CountToken, constant.StreamingTimeout = true, true, 30
	common.QuotaPerUnit = 500000
	gin.SetMode(gin.TestMode)
	settings := model_setting.GetGlobalSettings()
	oldPassThrough := settings.PassThroughRequestEnabled
	settings.PassThroughRequestEnabled = false
	t.Cleanup(func() {
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = oldRedis, oldMemory, oldBatch
		common.LogConsumeEnabled, constant.CountToken, constant.StreamingTimeout = oldConsume, oldCount, oldTimeout
		common.QuotaPerUnit = oldQuota
		gin.SetMode(oldMode)
		settings.PassThroughRequestEnabled = oldPassThrough
	})
	policy, policyErr := walletport.Default()
	if errors.Is(policyErr, walletport.ErrNotConfigured) {
		require.NoError(t, walletmodule.RegisterRuntime())
	} else {
		require.NoError(t, policyErr)
		require.IsType(t, walletmodule.Policy{}, policy)
	}
	factories := moneyport.NewFactoryRegistry()
	require.NoError(t, service.RegisterHostFundingFactories(factories))
	require.NoError(t, walletmodule.RegisterFundingFactory(factories))
	if err := service.ConfigureFundingFactoryRegistry(factories); err != nil {
		require.EqualError(t, err, "service: funding factory registry already configured")
	}
	const toolPriceKey = dto.BuildInToolWebSearchPreview + ":native-responses-fixed*"
	operation_setting.SetToolPriceForTest(toolPriceKey, 1)
	t.Cleanup(func() { operation_setting.DeleteToolPriceForTest(toolPriceKey) })

	for _, dialect := range []struct {
		kind            common.DatabaseType
		mainEnv, logEnv string
	}{
		{common.DatabaseTypeSQLite, "", ""},
		{common.DatabaseTypeMySQL, "TEST_FIXED_MYSQL_DSN", "TEST_FIXED_MYSQL_LOG_DSN"},
		{common.DatabaseTypePostgreSQL, "TEST_FIXED_POSTGRES_DSN", "TEST_FIXED_POSTGRES_LOG_DSN"},
	} {
		t.Run(string(dialect.kind), func(t *testing.T) {
			db, logDB := convertedStreamAccountingDB(t, dialect.kind, dialect.mainEnv, dialect.logEnv)
			for _, transport := range []string{"http_sse", "websocket_accumulator"} {
				for _, tc := range []struct {
					name      string
					events    []string
					wantQuota int
				}{
					{"leading failed", []string{`{"type":"response.failed","response":{"status":"failed"}}`}, 0},
					{"leading error", []string{`{"type":"error","code":"server_error","message":"failed"}`}, 0},
					{"error request echo is not output", []string{`{"type":"error","text":"echoed request","arguments":"echoed arguments","input":"echoed input"}`}, 0},
					{"created then response error", []string{`{"type":"response.created"}`, `{"type":"response.error","message":"failed"}`}, 0},
					{"completed with failed status", []string{`{"type":"response.completed","response":{"status":"failed"}}`}, 0},
					{"done with cancelled status", []string{`{"type":"response.done","response":{"status":"cancelled"}}`}, 0},
					{"partial text failure", []string{`{"type":"response.output_text.delta","delta":"partial answer"}`, `{"type":"response.failed"}`}, 5000},
					{"reported usage failure", []string{`{"type":"response.failed","response":{"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}}`}, 5000},
					{"cached usage failure", []string{`{"type":"response.failed","response":{"usage":{"input_tokens_details":{"cached_tokens":3}}}}`}, 5000},
					{"completed tool failure", []string{`{"type":"response.output_item.done","output_index":0,"item":{"id":"search-1","type":"web_search_call","status":"completed"}}`, `{"type":"response.failed"}`}, 5500},
					{"completed tool cancellation", []string{`{"type":"response.output_item.done","output_index":0,"item":{"id":"search-1","type":"web_search_call","status":"completed"}}`, `{"type":"response.cancelled"}`}, 5500},
					{"done only function arguments failure", []string{`{"type":"response.output_item.done","item":{"id":"fn-1","type":"function_call","name":"unpriced_fn","arguments":"{}"}}`, `{"type":"response.failed"}`}, 5000},
					{"completed function without arguments failure", []string{`{"type":"response.output_item.done","item":{"id":"fn-1","type":"function_call","name":"unpriced_fn","status":"completed","arguments":""}}`, `{"type":"response.failed"}`}, 5000},
					{"completed computer action failure", []string{`{"type":"response.output_item.done","item":{"id":"computer-1","type":"computer_call","status":"completed","action":{"type":"click","x":1,"y":2}}}`, `{"type":"response.failed"}`}, 5000},
					{"partial computer action failure", []string{`{"type":"response.output_item.added","item":{"id":"computer-1","type":"computer_call","status":"in_progress","action":{"type":"click","x":1,"y":2}}}`, `{"type":"response.failed"}`}, 5000},
					{"partial patch operation failure", []string{`{"type":"response.output_item.added","item":{"id":"patch-1","type":"apply_patch_call","status":"in_progress","operation":{"type":"update_file","path":"test.txt","diff":"generated patch"}}}`, `{"type":"response.failed"}`}, 5000},
					{"empty action shell is not output", []string{`{"type":"response.output_item.added","item":{"id":"computer-1","type":"computer_call","status":"in_progress","action":{}}}`, `{"type":"response.failed"}`}, 0},
					{"failed empty tool is not output", []string{`{"type":"response.output_item.done","item":{"id":"fn-1","type":"function_call","name":"unpriced_fn","status":"failed","arguments":""}}`, `{"type":"response.failed"}`}, 0},
					{"in progress empty tool is not output", []string{`{"type":"response.output_item.added","item":{"id":"fn-1","type":"function_call","name":"unpriced_fn","status":"in_progress","arguments":""}}`, `{"type":"response.failed"}`}, 0},
					{"created response tool shell is not completed", []string{`{"type":"response.created","response":{"status":"in_progress","output":[{"id":"fn-1","type":"function_call","name":"unpriced_fn"}]}}`, `{"type":"response.failed"}`}, 0},
					{"custom tool input delta failure", []string{`{"type":"response.custom_tool_call_input.delta","delta":"generated tool input"}`, `{"type":"response.failed"}`}, 5000},
					{"mcp arguments done failure", []string{`{"type":"response.mcp_call_arguments.done","arguments":"{}"}`, `{"type":"response.failed"}`}, 5000},
					{"code interpreter delta failure", []string{`{"type":"response.code_interpreter_call_code.delta","delta":"print(1)"}`, `{"type":"response.failed"}`}, 5000},
					{"code interpreter done failure", []string{`{"type":"response.code_interpreter_call_code.done","code":"print(1)"}`, `{"type":"response.failed"}`}, 5000},
					{"generated audio delta failure", []string{`{"type":"response.audio.delta","delta":"YXVkaW8="}`, `{"type":"response.failed"}`}, 5000},
					{"audio transcript delta failure", []string{`{"type":"response.audio_transcript.delta","delta":"partial transcript"}`, `{"type":"response.failed"}`}, 5000},
					{"terminal function arguments failure", []string{`{"type":"response.failed","response":{"output":[{"type":"function_call","name":"unpriced_fn","arguments":"{}"}]}}`}, 5000},
					{"terminal reasoning failure", []string{`{"type":"response.failed","response":{"output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"partial reasoning"}]}]}}`}, 5000},
					{"encrypted reasoning failure", []string{`{"type":"response.failed","response":{"output":[{"type":"reasoning","summary":[],"encrypted_content":"opaque-generated-reasoning"}]}}`}, 5000},
					{"terminal refusal failure", []string{`{"type":"response.failed","response":{"output":[{"type":"message","content":[{"type":"refusal","refusal":"I cannot answer"}]}]}}`}, 5000},
					{"done only message failure", []string{`{"type":"response.output_item.done","item":{"type":"message","content":[{"type":"output_text","text":"partial answer"}]}}`, `{"type":"response.failed"}`}, 5000},
					{"successful missing usage", []string{`{"type":"response.completed","response":{"status":"completed"}}`}, 5000},
					{"created only disconnect", []string{`{"type":"response.created","response":{"status":"in_progress"}}`}, 5000},
					{"successful nil usage dispatch", nil, 5000},
				} {
					t.Run(transport+"/"+tc.name, func(t *testing.T) {
						c, info, user, token := nativeResponsesBillingReservation(t, db)
						var usage *dto.Usage
						if len(tc.events) > 0 {
							if transport == "http_sse" {
								body := "data: " + strings.Join(tc.events, "\n\ndata: ") + "\n\n"
								resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
								var apiErr *types.NewAPIError
								usage, apiErr = openai.OaiResponsesStreamHandler(c, info, resp)
								if tc.name != "successful missing usage" {
									assert.NotNil(t, apiErr)
									assert.True(t, types.IsSkipRetryError(apiErr))
								}
							} else {
								accumulator := service.NewResponsesUsageAccumulator(info)
								for _, raw := range tc.events {
									var event dto.ResponsesStreamResponse
									require.NoError(t, common.UnmarshalJsonStr(raw, &event))
									accumulator.Observe(&event, []byte(raw))
								}
								usage = accumulator.Finish()
								assert.Equal(t, usage, accumulator.Finish(), "finishing a call twice must not add tool usage")
							}
						}
						relay.ConsumeResponsesQuota(c, info, usage)
						assertNativeResponsesBillingBalance(t, db, user, token, tc.wantQuota)
						var logs []model.Log
						require.NoError(t, logDB.Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).Find(&logs).Error)
						if tc.wantQuota == 0 {
							assert.Empty(t, logs, "an empty explicit failure must not record a successful fixed-fee consumption")
						} else {
							require.Len(t, logs, 1)
							assert.Equal(t, tc.wantQuota, logs[0].Quota)
						}
						var ledger []model.BalanceLedger
						require.NoError(t, db.Where("user_id = ?", user.Id).Order("id").Find(&ledger).Error)
						require.NotEmpty(t, ledger, "the test must include a real wallet reservation")
						assert.Equal(t, -5000, ledger[0].Delta)
						net := 0
						for _, row := range ledger {
							assert.Equal(t, string(model.AccountPaid), row.Account)
							net += row.Delta
						}
						assert.Equal(t, -tc.wantQuota, net)
						require.NoError(t, info.Billing.Settle(tc.wantQuota))
						info.Billing.Refund(c)
						assertNativeResponsesBillingBalance(t, db, user, token, tc.wantQuota)
						var ledgerAfter int64
						require.NoError(t, db.Model(&model.BalanceLedger{}).Where("user_id = ?", user.Id).Count(&ledgerAfter).Error)
						assert.Equal(t, int64(len(ledger)), ledgerAfter, "settlement replay and outer refund must not move money twice")
					})
				}
			}
			for _, status := range []string{"failed", "error", "cancelled", "canceled"} {
				for _, tc := range []struct {
					name, fields string
					wantQuota    int
				}{
					{"empty", `"output":[]`, 0},
					{"echoed request input", `"input":"echoed request","output":[]`, 0},
					{"partial usage", `"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}`, 5000},
					{"completed tool", `"output":[{"id":"search-1","type":"web_search_call","status":"completed"}]`, 5500},
					{"unpriced arguments", `"output":[{"type":"function_call","name":"unpriced_fn","arguments":"{}"}]`, 5000},
				} {
					t.Run("http_nonstream/"+status+"/"+tc.name, func(t *testing.T) {
						body := `{"id":"resp_failed","status":"` + status + `","error":{"code":"server_error","message":"private provider failure"},` + tc.fields + `}`
						runNativeResponsesRequestBilling(t, db, logDB, body, false, tc.wantQuota, true, constant.ChannelTypeOpenAI)
					})
				}
			}
			t.Run("http_nonstream/success missing usage", func(t *testing.T) {
				runNativeResponsesRequestBilling(t, db, logDB, `{"status":"completed","output":[]}`, false, 5000, false, constant.ChannelTypeOpenAI)
			})
			for _, tc := range []struct {
				name, event string
				wantQuota   int
			}{
				{"empty failed", `{"type":"response.failed","response":{"status":"failed"}}`, 0},
				{"failed partial usage", `{"type":"response.failed","response":{"status":"failed","usage":{"input_tokens":10,"output_tokens":2}}}`, 5000},
				{"done with cancelled status", `{"type":"response.done","response":{"status":"cancelled"}}`, 0},
			} {
				t.Run("http_request/"+tc.name, func(t *testing.T) {
					runNativeResponsesRequestBilling(t, db, logDB, "data: "+tc.event+"\n\n", true, tc.wantQuota, true, constant.ChannelTypeOpenAI)
				})
			}
			for _, tc := range []struct{ name, body string }{
				{"empty candidates", `{"candidates":[],"usageMetadata":{"totalTokenCount":0}}`},
				{"blocked prompt", `{"candidates":[],"promptFeedback":{"blockReason":"SAFETY"}}`},
			} {
				t.Run("http_gemini/"+tc.name, func(t *testing.T) {
					runNativeResponsesRequestBilling(t, db, logDB, tc.body, false, 0, true, constant.ChannelTypeGemini)
				})
			}
			t.Run("zero settlement transaction failure recovers reserve", func(t *testing.T) {
				c, info, user, token := nativeResponsesBillingReservation(t, db)
				info.StreamStatus.MarkFailed("server_error", "", 0)
				var failed atomic.Bool
				refunded := make(chan struct{}, 1)
				callback := "native_zero_settlement_" + info.RequestId
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table == "tokens" && !failed.Swap(true) {
						tx.AddError(errors.New("injected token quota transaction failure"))
					}
				}))
				t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(callback)) })
				require.NoError(t, db.Callback().Update().After("gorm:commit_or_rollback_transaction").Register(callback+"_refunded", func(tx *gorm.DB) {
					if tx.Statement.Table == "tokens" && tx.Error == nil {
						select {
						case refunded <- struct{}{}:
						default:
						}
					}
				}))
				t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(callback+"_refunded")) })
				relay.ConsumeResponsesQuota(c, info, nil)
				require.True(t, failed.Load(), "the real wallet/token settlement must encounter the injected rollback")
				select {
				case <-refunded:
				case <-time.After(5 * time.Second):
					t.Fatal("failed zero settlement did not refund the reservation")
				}
				assertNativeResponsesBillingBalance(t, db, user, token, 0)
				info.Billing.Refund(c)
				assertNativeResponsesBillingBalance(t, db, user, token, 0)
				var net int
				require.NoError(t, db.Model(&model.BalanceLedger{}).Where("user_id = ?", user.Id).Select("SUM(delta)").Scan(&net).Error)
				assert.Zero(t, net, "the failed adjustment must roll back before the ordinary refund")
			})
		})
	}
}

func runNativeResponsesRequestBilling(t *testing.T, db, logDB *gorm.DB, body string, stream bool, wantQuota int, wantError bool, channelType int) {
	t.Helper()
	c, info, user, token := nativeResponsesBillingReservation(t, db)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		contentType := "application/json"
		if stream {
			contentType = "text/event-stream"
		}
		w.Header().Set("Content-Type", contentType)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(upstream.Close)
	info.IsStream = stream
	if !stream {
		info.StreamStatus = nil
	}
	info.RelayMode = relayconstant.RelayModeResponses
	info.RequestURLPath = "/v1/responses"
	info.Request = &dto.OpenAIResponsesRequest{Model: info.OriginModelName, Input: []byte(`"hi"`), Stream: &stream}
	for key, value := range map[constant.ContextKey]any{
		constant.ContextKeyChannelId: info.ChannelId, constant.ContextKeyChannelType: channelType,
		constant.ContextKeyChannelBaseUrl: upstream.URL, constant.ContextKeyChannelKey: "test-only",
		constant.ContextKeyOriginalModel: info.OriginModelName,
	} {
		common.SetContextKey(c, key, value)
	}
	refunded := make(chan struct{}, 1)
	callback := "native_request_refund_" + info.RequestId
	require.NoError(t, db.Callback().Update().After("gorm:commit_or_rollback_transaction").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" && tx.Error == nil {
			select {
			case refunded <- struct{}{}:
			default:
			}
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(callback)) })
	apiErr := relay.ResponsesHelper(c, info)
	if wantError {
		require.NotNil(t, apiErr)
		if channelType == constant.ChannelTypeOpenAI {
			assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
			assert.True(t, types.IsSkipRetryError(apiErr))
		} else {
			assert.Contains(t, []int{http.StatusBadRequest, http.StatusInternalServerError}, apiErr.StatusCode)
		}
	} else {
		require.Nil(t, apiErr)
	}
	relay.RefundFailedRequestBilling(c, info, apiErr)
	if wantQuota == 0 {
		select {
		case <-refunded:
		case <-time.After(5 * time.Second):
			t.Fatal("the native HTTP failure did not finish refunding its reservation")
		}
	}
	assert.Equal(t, int32(1), calls.Load())
	assertNativeResponsesBillingBalance(t, db, user, token, wantQuota)
	if wantError {
		assert.False(t, service.RequestPolicy(c).Successful, "partial settlement must not mark a failed response successful")
	}
	var logs []model.Log
	require.NoError(t, logDB.Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).Find(&logs).Error)
	if wantQuota == 0 {
		assert.Empty(t, logs)
	} else {
		require.Len(t, logs, 1)
		assert.Equal(t, wantQuota, logs[0].Quota)
		assert.Equal(t, stream, logs[0].IsStream)
	}
}

func nativeResponsesBillingReservation(t *testing.T, db *gorm.DB) (*gin.Context, *relaycommon.RelayInfo, model.User, model.Token) {
	t.Helper()
	identity := strings.ReplaceAll(uuid.NewString(), "-", "")
	user := model.User{Username: "native_" + identity, AffCode: identity, Quota: 50100, QuotaPaid: 50000, QuotaBonus: 100, Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "native-stream-" + identity, RemainQuota: 50000, Status: common.TokenStatusEnabled}
	require.NoError(t, db.Create(&token).Error)
	channel := model.Channel{Name: "native-" + identity, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Key: "test-only"}
	require.NoError(t, db.Create(&channel).Error)
	const expression = `tier("request", fixed(0.01))`
	request := &billingexpr.RequestInput{Body: []byte(`{}`)}
	cost, trace, err := billingexpr.RunExprWithRequest(expression, billingexpr.TokenParams{P: 8, Len: 8}, *request)
	require.NoError(t, err)
	reservation, err := billingexpr.QuotaRoundStrict(cost / 1_000_000 * common.QuotaPerUnit)
	require.NoError(t, err)
	require.Equal(t, 5000, reservation)
	info := &relaycommon.RelayInfo{
		RequestId: identity, UserId: user.Id, TokenId: token.Id, TokenKey: token.Key,
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: channel.Id, ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gpt-4o"},
		OriginModelName: "native-responses-fixed", UsingGroup: "default", UserGroup: "default",
		UserSetting: dto.UserSetting{BillingPreference: "wallet_only"}, ForcePreConsume: true,
		StartTime: time.Now(), IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAIResponses,
		StreamStatus:          relaycommon.NewStreamStatus(),
		PriceData:             hosttypes.PriceData{GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}},
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: expression, ExprHash: billingexpr.ExprHashString(expression), QuotaPerUnit: common.QuotaPerUnit, GroupRatio: 1, EstimatedTier: trace.MatchedTier, EstimatedBillingUnit: trace.BillingUnit, EstimatedFixedPrice: trace.FixedPrice, EstimatedQuotaAfterGroup: reservation},
		BillingRequestInput:   request,
	}
	info.SetEstimatePromptTokens(8)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set(common.RequestIdKey, identity)
	require.Nil(t, service.PreConsumeBilling(c, reservation, info))
	require.NotNil(t, info.Billing)
	return c, info, user, token
}

func assertNativeResponsesBillingBalance(t *testing.T, db *gorm.DB, user model.User, token model.Token, charge int) {
	t.Helper()
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 50000-charge, user.QuotaPaid)
	assert.Equal(t, 100, user.QuotaBonus)
	assert.Zero(t, user.QuotaReward)
	assert.Equal(t, 50100-charge, user.Quota)
	assert.Equal(t, 50000-charge, token.RemainQuota)
	assert.Equal(t, charge, token.UsedQuota)
}
