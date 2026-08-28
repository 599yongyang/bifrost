package tables

import (
	"strings"
	"time"

	"github.com/bytedance/sonic"
	bifrost "github.com/maximhq/bifrost/core"
	"gorm.io/gorm"
)

// TableRoutingRule represents a routing rule in the database
type TableRoutingRule struct {
	ID            string `gorm:"primaryKey;type:varchar(255)" json:"id"`
	ConfigHash    string `gorm:"type:varchar(255)" json:"config_hash"` // Hash of config.json version, used for change detection
	Name          string `gorm:"type:varchar(255);not null;uniqueIndex:idx_routing_rule_scope_name" json:"name"`
	Description   string `gorm:"type:text" json:"description"`
	Enabled       *bool  `gorm:"not null;default:true" json:"enabled,omitempty"` // nil = DB default (true); use EnabledValue() to read
	CelExpression string `gorm:"type:text;not null" json:"cel_expression"`

	// Routing Targets (output) — 1:many relationship; weights must sum to 1
	Targets []TableRoutingTarget `gorm:"foreignKey:RuleID;constraint:OnDelete:CASCADE" json:"targets"`

	Fallbacks            *string                     `gorm:"type:text" json:"-"`                 // JSON array of fallback chains
	ParsedFallbacks      []string                    `gorm:"-" json:"fallbacks,omitempty"`       // Parsed fallbacks from JSON
	ErrorFallbacks       *string                     `gorm:"type:text" json:"-"`                 // JSON array of error-aware fallback rules
	ParsedErrorFallbacks []TableRoutingErrorFallback `gorm:"-" json:"error_fallbacks,omitempty"` // Parsed error-aware fallback rules from JSON

	Query       *string        `gorm:"type:text" json:"-"`
	ParsedQuery map[string]any `gorm:"-" json:"query,omitempty"`

	// Scope: where this rule applies
	Scope   string  `gorm:"type:varchar(50);not null;uniqueIndex:idx_routing_rule_scope_name" json:"scope"` // "global" | "team" | "customer" | "virtual_key" | "user"
	ScopeID *string `gorm:"type:varchar(255);uniqueIndex:idx_routing_rule_scope_name" json:"scope_id"`      // nil for global, otherwise entity ID

	// Chaining
	ChainRule bool `gorm:"not null;default:false" json:"chain_rule"` // If true, re-evaluates routing chain after this rule matches

	// Execution
	Priority int `gorm:"type:int;not null;default:0;index" json:"priority"` // Lower = evaluated first within scope

	// Timestamps
	CreatedAt time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"index;not null" json:"updated_at"`
}

// TableRoutingErrorFallback defines a fallback chain that is selected only
// when the failed attempt matches its scenario or explicit error conditions.
// These persistence-layer types intentionally use provider-neutral primitives;
// runtime packages can translate them without configstore depending on routing.
type TableRoutingErrorFallback struct {
	Name       string                               `json:"name,omitempty"`
	Scenario   string                               `json:"scenario,omitempty"`
	Supplement *TableRoutingErrorFallbackSupplement `json:"supplement,omitempty"`
	When       TableRoutingErrorFallbackCondition   `json:"when,omitempty"`
	Fallbacks  []string                             `json:"fallbacks"`
}

type TableRoutingErrorFallbackCondition struct {
	Categories      []string `json:"categories,omitempty"`
	ErrorCodes      []string `json:"error_codes,omitempty"`
	ErrorTypes      []string `json:"error_types,omitempty"`
	StatusCodes     []int    `json:"status_codes,omitempty"`
	MessageContains []string `json:"message_contains,omitempty"`
}

type TableRoutingErrorFallbackSupplement struct {
	Providers          []string `json:"providers,omitempty"`
	ErrorCodes         []string `json:"error_codes,omitempty"`
	ErrorTypes         []string `json:"error_types,omitempty"`
	StatusCodes        []int    `json:"status_codes,omitempty"`
	MessageContainsAny []string `json:"message_contains_any,omitempty"`
}

// TableName for TableRoutingRule
func (TableRoutingRule) TableName() string { return "routing_rules" }

// EnabledValue returns the effective Enabled bool, treating nil as true (DB default).
func (r *TableRoutingRule) EnabledValue() bool {
	if r == nil {
		return false
	}
	if r.Enabled == nil {
		return true
	}
	return *r.Enabled
}

// BeforeSave hook for TableRoutingRule to serialize JSON fields
func (r *TableRoutingRule) BeforeSave(tx *gorm.DB) error {
	if len(r.ParsedFallbacks) > 0 {
		data, err := sonic.Marshal(r.ParsedFallbacks)
		if err != nil {
			return err
		}
		r.Fallbacks = bifrost.Ptr(string(data))
	} else {
		r.Fallbacks = nil
	}
	if r.ParsedErrorFallbacks != nil {
		if len(r.ParsedErrorFallbacks) == 0 {
			r.ErrorFallbacks = nil
		} else {
			data, err := sonic.Marshal(r.ParsedErrorFallbacks)
			if err != nil {
				return err
			}
			r.ErrorFallbacks = bifrost.Ptr(string(data))
		}
	} else if r.ErrorFallbacks != nil && strings.TrimSpace(*r.ErrorFallbacks) != "" {
		// Preserve raw-only records. This path is used by migrations and callers
		// that deliberately avoid binding to the error-fallback runtime schema.
		var parsed []TableRoutingErrorFallback
		if err := sonic.Unmarshal([]byte(*r.ErrorFallbacks), &parsed); err != nil {
			return err
		}
	} else {
		r.ErrorFallbacks = nil
	}
	if r.ParsedQuery != nil {
		data, err := sonic.Marshal(r.ParsedQuery)
		if err != nil {
			return err
		}
		r.Query = bifrost.Ptr(string(data))
	} else {
		r.Query = nil
	}
	return nil
}

// AfterFind hook for TableRoutingRule to deserialize JSON fields
func (r *TableRoutingRule) AfterFind(tx *gorm.DB) error {
	if r.Fallbacks != nil && strings.TrimSpace(*r.Fallbacks) != "" {
		if err := sonic.Unmarshal([]byte(*r.Fallbacks), &r.ParsedFallbacks); err != nil {
			return err
		}
	}
	if r.ErrorFallbacks != nil && strings.TrimSpace(*r.ErrorFallbacks) != "" {
		if err := sonic.Unmarshal([]byte(*r.ErrorFallbacks), &r.ParsedErrorFallbacks); err != nil {
			return err
		}
	}
	if r.Query != nil && strings.TrimSpace(*r.Query) != "" {
		if err := sonic.Unmarshal([]byte(*r.Query), &r.ParsedQuery); err != nil {
			return err
		}
	}
	return nil
}

// TableRoutingTarget represents a weighted routing target for probabilistic routing.
// Multiple targets can be associated with a single routing rule; weights determine
// the probability of each target being selected and must sum to 1 across all targets in a rule.
// The composite (RuleID, Provider, Model, KeyID) is unique to prevent duplicate target configs.
type TableRoutingTarget struct {
	RuleID          string  `gorm:"type:varchar(255);not null;index;uniqueIndex:idx_routing_target_config" json:"-"`
	Provider        *string `gorm:"type:varchar(255);uniqueIndex:idx_routing_target_config" json:"provider,omitempty"` // nil = use incoming provider
	Model           *string `gorm:"type:varchar(255);uniqueIndex:idx_routing_target_config" json:"model,omitempty"`    // nil = use incoming model
	KeyID           *string `gorm:"type:varchar(255);uniqueIndex:idx_routing_target_config" json:"key_id,omitempty"`   // persisted key pin
	ProviderKeyName *string `gorm:"-" json:"provider_key_name,omitempty"`                                              // config-only alias; resolved to key_id during load
	Weight          float64 `gorm:"not null;default:1" json:"weight"`                                                  // must sum to 1 across all targets in a rule
}

// TableName for TableRoutingTarget
func (TableRoutingTarget) TableName() string { return "routing_targets" }
