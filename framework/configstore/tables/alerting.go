package tables

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	coreSchemas "github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/encrypt"
	"gorm.io/gorm"
)

const (
	AlertChannelSlack          = "slack"
	AlertChannelMicrosoftTeams = "microsoft_teams"
	AlertChannelWeCom          = "wecom"
	AlertChannelPagerDuty      = "pagerduty"
	AlertChannelWebhook        = "webhook"
)

var AlertChannelTypes = []string{
	AlertChannelSlack,
	AlertChannelMicrosoftTeams,
	AlertChannelWeCom,
	AlertChannelPagerDuty,
	AlertChannelWebhook,
}

// TableAlertChannel is a durable notification destination. ConfigJSON is
// encrypted by the model hooks because it contains webhook URLs, routing keys,
// and arbitrary custom headers.
type TableAlertChannel struct {
	ID               string         `gorm:"type:varchar(255);primaryKey" json:"id"`
	Name             string         `gorm:"type:varchar(255);uniqueIndex;not null" json:"name"`
	Description      string         `gorm:"type:text" json:"description,omitempty"`
	Type             string         `gorm:"type:varchar(32);index;not null" json:"type"`
	Enabled          bool           `gorm:"not null;default:true" json:"enabled"`
	ConfigJSON       string         `gorm:"type:text;not null" json:"-"`
	EncryptionStatus string         `gorm:"type:varchar(20);default:'plain_text'" json:"-"`
	ManagedByConfig  bool           `gorm:"not null;default:false;index" json:"-"`
	CreatedAt        time.Time      `gorm:"index;not null" json:"created_at"`
	UpdatedAt        time.Time      `gorm:"index;not null" json:"updated_at"`
	Config           map[string]any `gorm:"-" json:"config"`
}

func (TableAlertChannel) TableName() string { return "alert_channels" }

func (c *TableAlertChannel) Validate() error {
	if c == nil {
		return fmt.Errorf("alert channel cannot be nil")
	}
	if c.Name == "" {
		return fmt.Errorf("name is required")
	}
	if !slices.Contains(AlertChannelTypes, c.Type) {
		return fmt.Errorf("unsupported alert channel type %q", c.Type)
	}
	if c.Config == nil {
		return fmt.Errorf("config is required")
	}
	return nil
}

func (c *TableAlertChannel) BeforeSave(*gorm.DB) error {
	raw, err := coreSchemas.MarshalSorted(c.Config)
	if err != nil {
		return err
	}
	c.ConfigJSON = string(raw)
	if encrypt.IsEnabled() && c.ConfigJSON != "" {
		c.ConfigJSON, err = encrypt.Encrypt(c.ConfigJSON)
		if err != nil {
			return fmt.Errorf("encrypt alert channel config: %w", err)
		}
		c.EncryptionStatus = EncryptionStatusEncrypted
	}
	return nil
}

func (c *TableAlertChannel) AfterFind(*gorm.DB) error {
	raw := c.ConfigJSON
	var err error
	if c.EncryptionStatus == EncryptionStatusEncrypted && raw != "" {
		raw, err = encrypt.Decrypt(raw)
		if err != nil {
			return fmt.Errorf("decrypt alert channel config: %w", err)
		}
	}
	if raw == "" {
		c.Config = map[string]any{}
		return nil
	}
	return json.Unmarshal([]byte(raw), &c.Config)
}

// Redacted returns an API-safe copy without delivery secrets.
func (c TableAlertChannel) Redacted() TableAlertChannel {
	c.Config = redactAlertConfigMap(c.Config)
	return c
}

func redactAlertConfigMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	redacted := make(map[string]any, len(source))
	for key, value := range source {
		switch nested := value.(type) {
		case map[string]any:
			redacted[key] = redactAlertConfigMap(nested)
		case map[string]string:
			copy := make(map[string]any, len(nested))
			for nestedKey := range nested {
				copy[nestedKey] = "***redacted***"
			}
			redacted[key] = copy
		default:
			redacted[key] = "***redacted***"
		}
	}
	return redacted
}

type TableAlertRule struct {
	ID                      string         `gorm:"type:varchar(255);primaryKey" json:"id"`
	Name                    string         `gorm:"type:varchar(255);uniqueIndex;not null" json:"name"`
	Description             string         `gorm:"type:text" json:"description,omitempty"`
	Enabled                 bool           `gorm:"not null;default:true" json:"enabled"`
	ScopeType               string         `gorm:"type:varchar(32);index;not null" json:"scope_type"`
	ScopeID                 string         `gorm:"type:varchar(255);index;not null" json:"scope_id"`
	TargetType              *string        `gorm:"type:varchar(32);index" json:"target_type,omitempty"`
	TargetID                *string        `gorm:"type:varchar(255);index" json:"target_id,omitempty"`
	CELExpression           string         `gorm:"type:text;not null" json:"cel_expression"`
	ChannelIDsJSON          string         `gorm:"type:text;not null" json:"-"`
	QueryBuilderJSON        string         `gorm:"type:text" json:"-"`
	CooldownMilliseconds    int64          `gorm:"not null;default:60000" json:"cooldown_milliseconds"`
	WindowSeconds           int64          `gorm:"not null;default:300" json:"window_seconds"`
	MinRequests             int64          `gorm:"not null;default:1" json:"min_requests"`
	NotifyOncePerResetCycle bool           `gorm:"not null;default:false" json:"notify_once_per_reset_cycle"`
	CreatedAt               time.Time      `gorm:"index;not null" json:"created_at"`
	UpdatedAt               time.Time      `gorm:"index;not null" json:"updated_at"`
	ChannelIDs              []string       `gorm:"-" json:"channel_ids"`
	QueryBuilder            map[string]any `gorm:"-" json:"query,omitempty"`
	ManagedByConfig         bool           `gorm:"not null;default:false;index" json:"-"`
}

func (TableAlertRule) TableName() string { return "alert_rules" }

func (r *TableAlertRule) BeforeSave(*gorm.DB) error {
	channelIDs, err := coreSchemas.MarshalSorted(r.ChannelIDs)
	if err != nil {
		return err
	}
	r.ChannelIDsJSON = string(channelIDs)
	if r.QueryBuilder != nil {
		builder, err := coreSchemas.MarshalSorted(r.QueryBuilder)
		if err != nil {
			return err
		}
		r.QueryBuilderJSON = string(builder)
	}
	return nil
}

func (r *TableAlertRule) AfterFind(*gorm.DB) error {
	if r.ChannelIDsJSON != "" {
		if err := json.Unmarshal([]byte(r.ChannelIDsJSON), &r.ChannelIDs); err != nil {
			return err
		}
	}
	if r.QueryBuilderJSON != "" {
		if err := json.Unmarshal([]byte(r.QueryBuilderJSON), &r.QueryBuilder); err != nil {
			return err
		}
	}
	return nil
}

// TableAlertCooldown is the cluster-shared successful-send state. It is kept
// separate from history so live cooldown checks and failover recovery are
// bounded by the number of rule targets, not by history volume.
type TableAlertCooldown struct {
	Key        string    `gorm:"type:text;primaryKey" json:"key"`
	LastSentAt time.Time `gorm:"index;not null" json:"last_sent_at"`
	UpdatedAt  time.Time `gorm:"not null" json:"updated_at"`
}

func (TableAlertCooldown) TableName() string { return "alert_cooldowns" }
