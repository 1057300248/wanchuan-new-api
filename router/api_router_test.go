package router

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestTokenMixP1RoutesRegistered 验证 TokenMix P1 wallet/referral 路由注册
// 无冲突且路径符合预期（静态段优先于 admin :id 参数段）。
func TestTokenMixP1RoutesRegistered(t *testing.T) {
	previousDB, previousLogDB := model.DB, model.LOG_DB
	db, err := gorm.Open(sqlite.Open("file:wallet-routes?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	model.DB, model.LOG_DB = db, db
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	// 路由注册本身不 panic 即代表无冲突（重复注册相同路径会 panic）。
	require.NotPanics(t, func() { SetApiRouter(engine) })

	routes := engine.Routes()
	paths := make(map[string]bool)
	for _, route := range routes {
		paths[route.Method+" "+route.Path] = true
	}
	expected := []string{
		http.MethodGet + " /api/user/wallet",
		http.MethodGet + " /api/user/wallet/ledger",
		http.MethodGet + " /api/user/referral/overview",
		http.MethodGet + " /api/user/referral/rewards",
		http.MethodGet + " /api/user/referral/withdrawals",
		http.MethodPost + " /api/user/referral/withdrawals",
		http.MethodPost + " /api/user/referral/mature",
		http.MethodGet + " /api/user/referral/admin/withdrawals",
		http.MethodPost + " /api/user/referral/admin/withdrawals/:id/approve",
		http.MethodPost + " /api/user/referral/admin/withdrawals/:id/reject",
		http.MethodPost + " /api/user/referral/admin/withdrawals/:id/pay",
	}
	for _, path := range expected {
		require.True(t, paths[path], "route not registered: %s", path)
	}
}
