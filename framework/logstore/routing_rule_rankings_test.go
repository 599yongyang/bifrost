package logstore

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoutingRuleRankingsCountsRequestsByOutcome(t *testing.T) {
	store, db := newFanoutTestStore(t)
	now := time.Now().UTC()
	ruleAID, ruleAName := "rule-a", "Primary traffic"
	ruleBID, ruleBName := "rule-b", "Fallback traffic"

	logs := []Log{
		{ID: "success-a", Timestamp: now, Object: "chat_completion", Provider: "openai", Model: "gpt-4", Status: "success", RoutingRuleID: &ruleAID, RoutingRuleName: &ruleAName},
		{ID: "error-a", Timestamp: now, Object: "chat_completion", Provider: "openai", Model: "gpt-4", Status: "error", RoutingRuleID: &ruleAID, RoutingRuleName: &ruleAName},
		{ID: "cancelled-a", Timestamp: now, Object: "chat_completion", Provider: "openai", Model: "gpt-4", Status: "cancelled", RoutingRuleID: &ruleAID, RoutingRuleName: &ruleAName},
		{ID: "success-b", Timestamp: now, Object: "chat_completion", Provider: "openai", Model: "gpt-4", Status: "success", RoutingRuleID: &ruleBID, RoutingRuleName: &ruleBName},
		{ID: "unmatched", Timestamp: now, Object: "chat_completion", Provider: "openai", Model: "gpt-4", Status: "success"},
	}
	for i := range logs {
		require.NoError(t, db.Create(&logs[i]).Error)
	}

	result, err := store.GetDimensionRankings(context.Background(), fanoutWindow(now), RankingDimensionRoutingRule)
	require.NoError(t, err)
	require.Len(t, result.Rankings, 2)

	byID := make(map[string]DimensionRankingWithTrend, len(result.Rankings))
	for _, ranking := range result.Rankings {
		byID[ranking.ID] = ranking
	}

	assert.Equal(t, ruleAName, byID[ruleAID].Name)
	assert.Equal(t, int64(3), byID[ruleAID].TotalRequests)
	assert.Equal(t, int64(1), byID[ruleAID].SuccessCount)
	assert.Equal(t, int64(1), byID[ruleAID].ErrorCount)
	assert.Equal(t, int64(1), byID[ruleBID].TotalRequests)
	assert.Equal(t, int64(1), byID[ruleBID].SuccessCount)
	assert.Equal(t, int64(0), byID[ruleBID].ErrorCount)
}
