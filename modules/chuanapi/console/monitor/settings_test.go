package monitor

import (
	"context"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroupNamesRemainOnSameTargetAfterUpstreamReorder(t *testing.T) {
	reordered := false
	s, _ := monitorFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		targets := `[{"id":"group-a","model":"alpha"},{"id":"group-b","model":"beta"}]`
		if reordered {
			targets = `[{"id":"group-b","model":"beta"},{"id":"group-a","model":"alpha"}]`
		}
		fmt.Fprintf(w, `{"success":true,"data":{"enabled":true,"server_time":1800000000,"targets":%s,"results":[]}}`, targets)
	})
	first, err := s.snapshot(context.Background())
	require.NoError(t, err)
	config := Config{Enabled: true, Title: "My monitor", Description: "Public reference", Groups: map[string]string{first.Targets[0].ID: "Premium"}}
	s.config = func() (Config, error) { return config, nil }
	reordered = true
	s.now = func() time.Time { return time.Unix(1800000040, 0) }
	second, err := s.snapshot(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "My monitor", second.Title)
	assert.Equal(t, "Public reference", second.Description)
	assert.Empty(t, second.Targets[0].Name)
	assert.Equal(t, "Premium", second.Targets[1].Name)
	assert.Equal(t, "alpha", second.Targets[1].Model)
	assert.Equal(t, first.Targets[0].ID, second.Targets[1].ID)
}

func TestRuntimeDisableBlocksCachedResultsAndEveryAsset(t *testing.T) {
	calls := 0
	s, router := monitorFixture(t, func(w http.ResponseWriter, r *http.Request) { calls++; writeSnapshot(w) })
	require.Equal(t, http.StatusOK, requestMonitor(router, "").Code)
	s.config = func() (Config, error) { return Config{Enabled: false}, nil }
	response := requestMonitor(router, "")
	assert.Contains(t, response.Body.String(), `"enabled":false`)
	for _, kind := range []string{"results", "artworks", "thumbnails"} {
		assert.Equal(t, http.StatusNotFound, requestMonitor(router, "/"+kind+"/"+fixtureID).Code)
	}
	assert.Equal(t, 1, calls)
}

func TestSettingsValidateLengthsAndStableGroupKeys(t *testing.T) {
	id := "route-" + strings.Repeat("a", 32)
	for _, tc := range []struct {
		name    string
		config  Config
		invalid bool
	}{
		{"trim and clear", Config{Groups: map[string]string{id: "  "}, Title: " My page "}, false},
		{"unicode name", Config{Groups: map[string]string{id: strings.Repeat("组", 80)}}, false},
		{"long name", Config{Groups: map[string]string{id: strings.Repeat("组", 81)}}, true},
		{"bad hidden identity", Config{HiddenGroups: map[string]bool{"route-7": true}}, true},
		{"visible entry removed", Config{HiddenGroups: map[string]bool{id: false}}, false},
		{"bad identity", Config{Groups: map[string]string{"route-1": "Name"}}, true},
		{"long title", Config{Title: strings.Repeat("x", 121)}, true},
		{"long description", Config{Description: strings.Repeat("x", 501)}, true},
		{"control characters", Config{Groups: map[string]string{id: "bad\x00name"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.config.normalize()
			if tc.invalid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tc.name == "visible entry removed" {
				assert.Empty(t, tc.config.HiddenGroups)
			}
			if tc.name == "trim and clear" {
				assert.Empty(t, tc.config.Groups)
				assert.Equal(t, "My page", tc.config.Title)
			}
		})
	}
}

func TestHiddenGroupsBlockCachedResultsAndAssetsButRemainConfigurable(t *testing.T) {
	calls := 0
	s, router := monitorFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"success":true,"data":{"enabled":true,"server_time":1800000000,"targets":[{"id":"group-1","model":"alpha"},{"id":"group-2","model":"beta"}],"results":[{"id":%q,"target_id":"group-1","benchmark":"pelican","status":"ready","completed_at":1799999990}]}}`, fixtureID)
	})
	first, err := s.snapshot(context.Background())
	require.NoError(t, err)
	require.Len(t, first.Targets, 2)
	hiddenID := first.Targets[0].ID
	config := Config{Enabled: true, Groups: map[string]string{hiddenID: "Premium"}, HiddenGroups: map[string]bool{hiddenID: true}}
	s.config = func() (Config, error) { return config, nil }
	filtered, err := s.snapshot(context.Background())
	require.NoError(t, err)
	require.Len(t, filtered.Targets, 1)
	assert.Equal(t, first.Targets[1], filtered.Targets[0])
	assert.Empty(t, filtered.Results)
	for _, kind := range []string{"results", "artworks", "thumbnails"} {
		assert.Equal(t, http.StatusNotFound, requestMonitor(router, "/"+kind+"/"+fixtureID).Code)
	}
	s.RegisterSettings(router.Group("/api/option")) // Authentication is covered by the PostgreSQL integration fixture.
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/option/model-monitor/targets", nil))
	require.Equal(t, http.StatusOK, response.Code)
	var catalog envelope[[]Target]
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &catalog))
	assert.Equal(t, first.Targets, catalog.Data)
	config.HiddenGroups = map[string]bool{}
	restored, err := s.snapshot(context.Background())
	require.NoError(t, err)
	require.Len(t, restored.Targets, 2)
	assert.Equal(t, hiddenID, restored.Targets[0].ID)
	assert.Equal(t, "Premium", restored.Targets[0].Name)
	assert.Equal(t, first.Results, restored.Results)
	assert.Equal(t, 1, calls, "visibility changes apply to the cached snapshot without fetching assets")
}
