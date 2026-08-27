package configstore

import (
	"testing"

	"github.com/bytedance/sonic"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateRoutingRuleHash_ErrorFallbacksAffectDigest(t *testing.T) {
	rule := *routingRuleFixture("hash-error-fallbacks", 0, "openai")

	baseHash, err := GenerateRoutingRuleHash(rule)
	require.NoError(t, err)

	rule.ParsedErrorFallbacks = []tables.TableRoutingErrorFallback{{
		Name: "content policy",
		When: tables.TableRoutingErrorFallbackCondition{
			Categories: []string{"content_policy"},
		},
		Fallbacks: []string{"azure/gpt-image-1"},
	}}
	withErrorFallbacksHash, err := GenerateRoutingRuleHash(rule)
	require.NoError(t, err)

	assert.NotEqual(t, baseHash, withErrorFallbacksHash)
}

func TestGenerateRoutingRuleHash_ErrorFallbacksUsesCanonicalBytes(t *testing.T) {
	rule := *routingRuleFixture("hash-error-fallbacks-canonical", 0, "openai")
	rule.ParsedErrorFallbacks = []tables.TableRoutingErrorFallback{{
		Name: "content policy",
		When: tables.TableRoutingErrorFallbackCondition{
			Categories:      []string{"content_policy"},
			MessageContains: []string{"unsafe"},
		},
		Fallbacks: []string{"azure/gpt-image-1", "openai/gpt-image-1"},
	}}

	parsedHash, err := GenerateRoutingRuleHash(rule)
	require.NoError(t, err)

	rawJSON, err := sonic.Marshal(rule.ParsedErrorFallbacks)
	require.NoError(t, err)
	rule.ErrorFallbacks = ptrString(string(rawJSON))
	rule.ParsedErrorFallbacks = nil

	rawHash, err := GenerateRoutingRuleHash(rule)
	require.NoError(t, err)

	assert.Equal(t, parsedHash, rawHash)
}

func ptrString(s string) *string { return &s }
