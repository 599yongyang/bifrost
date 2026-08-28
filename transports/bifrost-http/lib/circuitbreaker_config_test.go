package lib

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/transports/bifrost-http/circuitbreaker"
	"github.com/stretchr/testify/require"
)

func TestConfigDataParsesRootCircuitBreakerConfig(t *testing.T) {
	var data ConfigData
	err := json.Unmarshal([]byte(`{
		"circuit_breaker_config":{"policies":[{
			"name":"azure-ptu-spillover","primary_provider":"azure","primary_model":"gpt-4o-ptu",
			"fallback_provider":"azure","fallback_model":"gpt-4o-paygo",
			"condition":{"signals":[{"source":"response_header","header_name":"X-Ms-Is-Spilled-Over","header_value":"true"}]},
			"default_cooldown":"30s"
		}]}
	}`), &data)
	require.NoError(t, err)
	require.NotNil(t, data.CircuitBreaker)
	require.Len(t, data.CircuitBreaker.Policies, 1)
	require.Equal(t, schemas.Azure, data.CircuitBreaker.Policies[0].PrimaryProvider)
	require.Nil(t, data.CircuitBreaker.Policies[0].Enabled)
	require.True(t, data.sectionPresent("circuit_breaker_config"))
}

func TestPromoteCircuitBreakerConfigToPlugin(t *testing.T) {
	data := ConfigData{
		CircuitBreaker:  &circuitbreaker.Config{Policies: []circuitbreaker.Policy{{Name: "from-file"}}},
		presentSections: map[string]bool{"circuit_breaker_config": true},
	}
	promoteCircuitBreakerConfigToPlugin(&data)
	require.Len(t, data.Plugins, 1)
	require.Equal(t, circuitbreaker.PluginName, data.Plugins[0].Name)
	require.True(t, data.Plugins[0].Enabled)
	require.Equal(t, schemas.PluginPlacementPostBuiltin, *data.Plugins[0].Placement)

	promoteCircuitBreakerConfigToPlugin(&data)
	require.Len(t, data.Plugins, 1)

	explicit := &schemas.PluginConfig{Name: circuitbreaker.PluginName, Enabled: true, Config: map[string]any{"marker": "explicit"}}
	data = ConfigData{
		CircuitBreaker:  &circuitbreaker.Config{Policies: []circuitbreaker.Policy{{Name: "root"}}},
		Plugins:         []*schemas.PluginConfig{explicit},
		presentSections: map[string]bool{"circuit_breaker_config": true, "plugins": true},
	}
	promoteCircuitBreakerConfigToPlugin(&data)
	require.Len(t, data.Plugins, 1)
	require.Same(t, explicit, data.Plugins[0], "explicit plugins[] entry must win over root promotion")
}

func TestCircuitBreakerFileConfigOverridesStoredPlugin(t *testing.T) {
	data := ConfigData{
		CircuitBreaker:  &circuitbreaker.Config{Policies: []circuitbreaker.Policy{{Name: "from-file"}}},
		presentSections: map[string]bool{"circuit_breaker_config": true},
	}
	promoteCircuitBreakerConfigToPlugin(&data)
	config := &Config{PluginConfigs: []*schemas.PluginConfig{{
		Name: circuitbreaker.PluginName, Enabled: true,
		Config: map[string]any{"policies": []any{map[string]any{"name": "stale-db"}}},
	}}}

	applyCircuitBreakerFileConfig(context.Background(), config, &data)
	require.Len(t, config.PluginConfigs, 1)
	require.Same(t, data.Plugins[0], config.PluginConfigs[0])
}

func TestEmptyCircuitBreakerFileConfigDisablesStoredPlugin(t *testing.T) {
	data := ConfigData{
		CircuitBreaker:  &circuitbreaker.Config{},
		presentSections: map[string]bool{"circuit_breaker_config": true},
	}
	promoteCircuitBreakerConfigToPlugin(&data)
	require.Len(t, data.Plugins, 1)
	require.False(t, data.Plugins[0].Enabled)
}
