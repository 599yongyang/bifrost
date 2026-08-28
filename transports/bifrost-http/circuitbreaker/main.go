// Package circuitbreaker provides header-signal-based provider failover.
package circuitbreaker

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

const PluginName = "circuit-breaker"

const defaultCooldown = 30 * time.Second

type Config struct {
	Policies []Policy `json:"policies"`
}

type Policy struct {
	Name             string                `json:"name"`
	Enabled          *bool                 `json:"enabled,omitempty"`
	PrimaryProvider  schemas.ModelProvider `json:"primary_provider"`
	PrimaryModel     string                `json:"primary_model"`
	PrimaryKeyIDs    []string              `json:"primary_key_ids,omitempty"`
	FallbackProvider schemas.ModelProvider `json:"fallback_provider"`
	FallbackModel    string                `json:"fallback_model"`
	Condition        Condition             `json:"condition"`
	DefaultCooldown  string                `json:"default_cooldown,omitempty"`
	CooldownHeader   string                `json:"cooldown_header,omitempty"`
}

type Condition struct {
	Operator string   `json:"operator,omitempty"`
	Signals  []Signal `json:"signals"`
}

type Signal struct {
	Source         string  `json:"source"`
	HeaderName     string  `json:"header_name"`
	HeaderValue    *string `json:"header_value,omitempty"`
	HeaderContains *string `json:"header_contains,omitempty"`
}

type circuit struct {
	openUntil     time.Time
	probeInFlight bool
}

type compiledPolicy struct {
	Policy
	cooldown time.Duration

	mu               sync.Mutex
	shared           circuit
	keys             map[string]*circuit
	keyProbeInFlight bool
}

type requestState struct {
	mu              sync.Mutex
	primaryProvider schemas.ModelProvider
	primaryModel    string
	routedPolicy    string
	probes          map[string]string
	evaluated       map[string]struct{}
}

type contextKey string

const requestStateKey contextKey = "circuit-breaker-request-state"

type Plugin struct {
	policies []*compiledPolicy
	now      func() time.Time
}

func Init(config *Config, _ schemas.Logger) (*Plugin, error) {
	if config == nil {
		config = &Config{}
	}
	plugin := &Plugin{now: time.Now}
	names := make(map[string]struct{}, len(config.Policies))
	for i := range config.Policies {
		policy, err := compilePolicy(config.Policies[i])
		if err != nil {
			return nil, fmt.Errorf("circuit breaker policy %d: %w", i, err)
		}
		if _, exists := names[policy.Name]; exists {
			return nil, fmt.Errorf("circuit breaker policy name %q is duplicated", policy.Name)
		}
		names[policy.Name] = struct{}{}
		plugin.policies = append(plugin.policies, policy)
	}
	return plugin, nil
}

func compilePolicy(raw Policy) (*compiledPolicy, error) {
	raw.Name = strings.TrimSpace(raw.Name)
	raw.PrimaryProvider = schemas.ModelProvider(strings.TrimSpace(string(raw.PrimaryProvider)))
	raw.PrimaryModel = strings.TrimSpace(raw.PrimaryModel)
	raw.FallbackProvider = schemas.ModelProvider(strings.TrimSpace(string(raw.FallbackProvider)))
	raw.FallbackModel = strings.TrimSpace(raw.FallbackModel)
	raw.CooldownHeader = strings.TrimSpace(raw.CooldownHeader)
	if raw.Name == "" || raw.PrimaryProvider == "" || raw.PrimaryModel == "" || raw.FallbackProvider == "" || raw.FallbackModel == "" {
		return nil, fmt.Errorf("name, primary_provider, primary_model, fallback_provider, and fallback_model are required")
	}
	if len(raw.Condition.Signals) == 0 {
		return nil, fmt.Errorf("condition.signals must contain at least one signal")
	}
	raw.Condition.Operator = strings.ToUpper(strings.TrimSpace(raw.Condition.Operator))
	if raw.Condition.Operator == "" {
		raw.Condition.Operator = "OR"
	}
	if raw.Condition.Operator != "OR" && raw.Condition.Operator != "AND" {
		return nil, fmt.Errorf("condition.operator must be OR or AND")
	}
	for i := range raw.Condition.Signals {
		signal := &raw.Condition.Signals[i]
		signal.Source = strings.ToLower(strings.TrimSpace(signal.Source))
		signal.HeaderName = strings.TrimSpace(signal.HeaderName)
		if signal.Source != "response_header" || signal.HeaderName == "" {
			return nil, fmt.Errorf("signal %d must use source response_header and a non-empty header_name", i)
		}
		if signal.HeaderValue != nil && signal.HeaderContains != nil {
			return nil, fmt.Errorf("signal %d cannot set both header_value and header_contains", i)
		}
	}
	cooldown := defaultCooldown
	if strings.TrimSpace(raw.DefaultCooldown) != "" {
		var err error
		cooldown, err = time.ParseDuration(strings.TrimSpace(raw.DefaultCooldown))
		if err != nil || cooldown <= 0 {
			return nil, fmt.Errorf("default_cooldown must be a positive Go duration")
		}
	}
	seen := make(map[string]struct{}, len(raw.PrimaryKeyIDs))
	keys := make(map[string]*circuit, len(raw.PrimaryKeyIDs))
	cleanIDs := make([]string, 0, len(raw.PrimaryKeyIDs))
	for _, id := range raw.PrimaryKeyIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, fmt.Errorf("primary_key_ids cannot contain an empty key ID")
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		keys[id] = &circuit{}
		cleanIDs = append(cleanIDs, id)
	}
	raw.PrimaryKeyIDs = cleanIDs
	return &compiledPolicy{Policy: raw, cooldown: cooldown, keys: keys}, nil
}

func (p *Plugin) GetName() string { return PluginName }
func (p *Plugin) Cleanup() error  { return nil }

func (p *Plugin) PreRequestHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) error {
	provider, model, _ := req.GetRequestFields()
	state := &requestState{
		primaryProvider: provider,
		primaryModel:    model,
		probes:          make(map[string]string),
		evaluated:       make(map[string]struct{}),
	}
	ctx.SetValue(requestStateKey, state)
	now := p.now()
	for _, policy := range p.matchingPolicies(provider, model) {
		if policy.isOpenForRequest(now) {
			req.SetProvider(policy.FallbackProvider)
			req.SetModel(policy.FallbackModel)
			state.routedPolicy = policy.Name
			schemas.AppendToContextList(ctx, schemas.BifrostContextKeyRoutingEnginesUsed, schemas.RoutingEngineCircuitBreaker)
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineCircuitBreaker, schemas.LogLevelInfo, fmt.Sprintf("Policy %s circuit is open; rerouted %s/%s to %s/%s", policy.Name, provider, model, policy.FallbackProvider, policy.FallbackModel))
			return nil
		}
	}
	return nil
}

func (p *Plugin) PreLLMHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	state, _ := ctx.Value(requestStateKey).(*requestState)
	if state == nil {
		return req, nil, nil
	}
	now := p.now()
	for _, policy := range p.matchingPolicies(state.primaryProvider, state.primaryModel) {
		if len(policy.keys) > 0 {
			continue
		}
		if probeKey, owned := state.getProbe(policy.Name); owned && probeKey == "" {
			continue
		}
		if policy.sharedStatus(now) == keyProbeReady && policy.beginSharedProbe(now) {
			state.setProbe(policy.Name, "")
		}
	}
	return req, nil, nil
}

func (p *Plugin) PostLLMHook(ctx *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	state, _ := ctx.Value(requestStateKey).(*requestState)
	if state == nil || state.routedPolicy != "" {
		return resp, bifrostErr, nil
	}
	provider, model := responseTarget(resp, bifrostErr)
	if provider != state.primaryProvider || model != state.primaryModel {
		return resp, bifrostErr, nil
	}
	headers := responseHeaders(ctx, resp, bifrostErr)
	now := p.now()
	selectedKeyID, _ := ctx.Value(schemas.BifrostContextKeySelectedKeyID).(string)
	for _, policy := range p.matchingPolicies(provider, model) {
		if !state.markEvaluated(policy.Name) {
			continue
		}
		if policy.matches(headers) {
			cooldown := policy.cooldownFor(headers)
			policy.trip(selectedKeyID, now.Add(cooldown))
			schemas.AppendToContextList(ctx, schemas.BifrostContextKeyRoutingEnginesUsed, schemas.RoutingEngineCircuitBreaker)
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineCircuitBreaker, schemas.LogLevelWarn, fmt.Sprintf("Policy %s signal matched; circuit opened for %s", policy.Name, cooldown))
			continue
		}
		if probeKey, probing := state.popProbe(policy.Name); probing {
			if bifrostErr != nil || resp == nil {
				cooldown := policy.cooldownFor(headers)
				policy.trip(probeKey, now.Add(cooldown))
				ctx.AppendRoutingEngineLog(schemas.RoutingEngineCircuitBreaker, schemas.LogLevelWarn, fmt.Sprintf("Policy %s probe failed; circuit remains open for %s", policy.Name, cooldown))
			} else {
				policy.closeProbe(probeKey)
				ctx.AppendRoutingEngineLog(schemas.RoutingEngineCircuitBreaker, schemas.LogLevelInfo, fmt.Sprintf("Policy %s probe succeeded without matching its signal; circuit closed", policy.Name))
			}
		}
	}
	return resp, bifrostErr, nil
}

// FilterKeys removes cooling-down key sub-circuits and admits at most one
// half-open probe after cooldown expiry.
func (p *Plugin) FilterKeys(ctx *schemas.BifrostContext, provider schemas.ModelProvider, model string, keys []schemas.Key) ([]schemas.Key, error) {
	policies := p.matchingPolicies(provider, model)
	if len(policies) == 0 {
		return keys, nil
	}
	state, _ := ctx.Value(requestStateKey).(*requestState)
	if state == nil {
		return keys, nil
	}
	now := p.now()

	for _, policy := range policies {
		if keyID, owned := state.getProbe(policy.Name); owned && keyID != "" {
			for _, key := range keys {
				if key.ID == keyID {
					return []schemas.Key{key}, nil
				}
			}
			return nil, nil
		}
	}
	for _, policy := range policies {
		if len(policy.keys) > 0 && policy.isOpenForRequest(now) {
			return nil, nil
		}
	}
	for _, policy := range policies {
		if len(policy.keys) != 0 {
			continue
		}
		if probeKey, owned := state.getProbe(policy.Name); owned && probeKey == "" {
			continue
		}
		if policy.sharedStatus(now) == keySuppressed {
			return nil, nil
		}
	}

	var probe *schemas.Key
	filtered := make([]schemas.Key, 0, len(keys))
	for i := range keys {
		key := keys[i]
		suppressed := false
		probePolicies := make([]*compiledPolicy, 0, len(policies))
		for _, policy := range policies {
			status := policy.keyStatus(key.ID, now)
			if status == keySuppressed {
				suppressed = true
				break
			}
			if status == keyProbeReady {
				probePolicies = append(probePolicies, policy)
			}
		}
		if suppressed {
			continue
		}
		if probe == nil && len(probePolicies) > 0 {
			claimed := make([]*compiledPolicy, 0, len(probePolicies))
			for _, policy := range probePolicies {
				if !policy.beginKeyProbe(key.ID, now) {
					for _, claimedPolicy := range claimed {
						claimedPolicy.cancelKeyProbe(key.ID)
						state.popProbe(claimedPolicy.Name)
					}
					claimed = nil
					break
				}
				state.setProbe(policy.Name, key.ID)
				claimed = append(claimed, policy)
			}
			if len(claimed) > 0 {
				candidate := key
				probe = &candidate
			}
			continue
		}
		filtered = append(filtered, key)
	}
	if probe != nil {
		return []schemas.Key{*probe}, nil
	}
	return filtered, nil
}

func (p *Plugin) matchingPolicies(provider schemas.ModelProvider, model string) []*compiledPolicy {
	var matches []*compiledPolicy
	for _, policy := range p.policies {
		if (policy.Enabled == nil || *policy.Enabled) && policy.PrimaryProvider == provider && policy.PrimaryModel == model {
			matches = append(matches, policy)
		}
	}
	return matches
}

type keyCircuitStatus uint8

const (
	keyHealthy keyCircuitStatus = iota
	keySuppressed
	keyProbeReady
)

func (policy *compiledPolicy) isOpenForRequest(now time.Time) bool {
	policy.mu.Lock()
	defer policy.mu.Unlock()
	if len(policy.keys) > 0 {
		hasExpired := false
		for _, circuit := range policy.keys {
			if circuit.openUntil.IsZero() {
				return false
			}
			if !now.Before(circuit.openUntil) && !circuit.probeInFlight {
				hasExpired = true
			}
		}
		return !hasExpired || policy.keyProbeInFlight
	}
	if policy.shared.openUntil.IsZero() {
		return false
	}
	return now.Before(policy.shared.openUntil) || policy.shared.probeInFlight
}

func (policy *compiledPolicy) keyStatus(id string, now time.Time) keyCircuitStatus {
	policy.mu.Lock()
	defer policy.mu.Unlock()
	circuit, tracked := policy.keys[id]
	if !tracked || circuit.openUntil.IsZero() {
		return keyHealthy
	}
	if now.Before(circuit.openUntil) || circuit.probeInFlight {
		return keySuppressed
	}
	return keyProbeReady
}

func (policy *compiledPolicy) beginKeyProbe(id string, now time.Time) bool {
	policy.mu.Lock()
	defer policy.mu.Unlock()
	circuit, tracked := policy.keys[id]
	if !tracked || circuit.openUntil.IsZero() || now.Before(circuit.openUntil) || circuit.probeInFlight || policy.keyProbeInFlight {
		return false
	}
	circuit.probeInFlight = true
	policy.keyProbeInFlight = true
	return true
}

func (policy *compiledPolicy) cancelKeyProbe(id string) {
	policy.mu.Lock()
	defer policy.mu.Unlock()
	if circuit, ok := policy.keys[id]; ok {
		circuit.probeInFlight = false
	}
	policy.keyProbeInFlight = false
}

func (policy *compiledPolicy) sharedStatus(now time.Time) keyCircuitStatus {
	policy.mu.Lock()
	defer policy.mu.Unlock()
	if policy.shared.openUntil.IsZero() {
		return keyHealthy
	}
	if now.Before(policy.shared.openUntil) || policy.shared.probeInFlight {
		return keySuppressed
	}
	return keyProbeReady
}

func (policy *compiledPolicy) beginSharedProbe(now time.Time) bool {
	policy.mu.Lock()
	defer policy.mu.Unlock()
	if policy.shared.openUntil.IsZero() || now.Before(policy.shared.openUntil) || policy.shared.probeInFlight {
		return false
	}
	policy.shared.probeInFlight = true
	return true
}

func (policy *compiledPolicy) trip(keyID string, until time.Time) {
	policy.mu.Lock()
	defer policy.mu.Unlock()
	if len(policy.keys) == 0 {
		policy.shared.openUntil = until
		policy.shared.probeInFlight = false
		return
	}
	if circuit, ok := policy.keys[keyID]; ok {
		circuit.openUntil = until
		circuit.probeInFlight = false
	}
	policy.keyProbeInFlight = false
}

func (policy *compiledPolicy) closeProbe(keyID string) {
	policy.mu.Lock()
	defer policy.mu.Unlock()
	if len(policy.keys) == 0 {
		policy.shared = circuit{}
		return
	}
	if c, ok := policy.keys[keyID]; ok {
		*c = circuit{}
	}
	policy.keyProbeInFlight = false
}

func (policy *compiledPolicy) matches(headers map[string]string) bool {
	matched := policy.Condition.Operator == "AND"
	for _, signal := range policy.Condition.Signals {
		value, present := header(headers, signal.HeaderName)
		current := present
		if signal.HeaderValue != nil {
			current = present && strings.EqualFold(value, *signal.HeaderValue)
		} else if signal.HeaderContains != nil {
			current = present && strings.Contains(strings.ToLower(value), strings.ToLower(*signal.HeaderContains))
		}
		if policy.Condition.Operator == "OR" && current {
			return true
		}
		if policy.Condition.Operator == "AND" && !current {
			return false
		}
	}
	return matched
}

func (policy *compiledPolicy) cooldownFor(headers map[string]string) time.Duration {
	if policy.CooldownHeader != "" {
		if value, ok := header(headers, policy.CooldownHeader); ok {
			milliseconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			if err == nil && milliseconds > 0 && milliseconds <= int64(time.Duration(1<<63-1)/time.Millisecond) {
				return time.Duration(milliseconds) * time.Millisecond
			}
		}
	}
	return policy.cooldown
}

func header(headers map[string]string, name string) (string, bool) {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value, true
		}
	}
	return "", false
}

func responseHeaders(ctx *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) map[string]string {
	if resp != nil {
		if extraFields := resp.GetExtraFields(); extraFields != nil && extraFields.ProviderResponseHeaders != nil {
			return extraFields.ProviderResponseHeaders
		}
	}
	if headers, _ := ctx.Value(schemas.BifrostContextKeyProviderResponseHeaders).(map[string]string); headers != nil {
		return headers
	}
	if bifrostErr != nil {
		return bifrostErr.ExtraFields.UpstreamResponseHeaders
	}
	return nil
}

func (state *requestState) setProbe(policyName, keyID string) {
	state.mu.Lock()
	state.probes[policyName] = keyID
	state.mu.Unlock()
}

func (state *requestState) popProbe(policyName string) (string, bool) {
	state.mu.Lock()
	defer state.mu.Unlock()
	keyID, ok := state.probes[policyName]
	if ok {
		delete(state.probes, policyName)
	}
	return keyID, ok
}

func (state *requestState) getProbe(policyName string) (string, bool) {
	state.mu.Lock()
	defer state.mu.Unlock()
	keyID, ok := state.probes[policyName]
	return keyID, ok
}

func (state *requestState) markEvaluated(policyName string) bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	if _, exists := state.evaluated[policyName]; exists {
		return false
	}
	state.evaluated[policyName] = struct{}{}
	return true
}

func responseTarget(resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (schemas.ModelProvider, string) {
	if resp != nil && resp.GetExtraFields() != nil {
		info := resp.GetExtraFields().RoutingInfo
		return info.Provider, info.Model
	}
	if bifrostErr != nil {
		return bifrostErr.ExtraFields.RoutingInfo.Provider, bifrostErr.ExtraFields.RoutingInfo.Model
	}
	return "", ""
}
