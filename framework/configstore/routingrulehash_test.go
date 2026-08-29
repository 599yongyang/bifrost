package configstore

import (
	"testing"

	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateRoutingRuleHashErrorFallbacks(t *testing.T) {
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
	parsedHash, err := GenerateRoutingRuleHash(rule)
	require.NoError(t, err)
	assert.NotEqual(t, baseHash, parsedHash)

	// Raw DB JSON may have different whitespace and object-key ordering. Its
	// digest must still match the config-origin parsed representation.
	raw := `[ { "fallbacks" : ["azure/gpt-image-1"], "when" : {"categories":["content_policy"]}, "name":"content policy" } ]`
	rule.ErrorFallbacks = &raw
	rule.ParsedErrorFallbacks = nil
	rawHash, err := GenerateRoutingRuleHash(rule)
	require.NoError(t, err)
	assert.Equal(t, parsedHash, rawHash)
}

func TestGenerateRoutingRuleHashRejectsInvalidRawErrorFallbacks(t *testing.T) {
	rule := *routingRuleFixture("hash-invalid-error-fallbacks", 0, "openai")
	raw := `[{invalid]`
	rule.ErrorFallbacks = &raw

	_, err := GenerateRoutingRuleHash(rule)
	require.ErrorContains(t, err, "invalid error_fallbacks JSON")
}
