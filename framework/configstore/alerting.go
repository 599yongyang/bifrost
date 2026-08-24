package configstore

import (
	"context"
	"errors"
	"time"

	"github.com/maximhq/bifrost/framework/configstore/tables"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AlertStore is intentionally separate from ConfigStore. Keeping this feature
// interface narrow lets embedders provide their own alert configuration and cooldown persistence without
// forcing every ConfigStore test double to grow alert-only methods.
type AlertStore interface {
	ListAlertChannels(context.Context) ([]tables.TableAlertChannel, error)
	GetAlertChannel(context.Context, string) (*tables.TableAlertChannel, error)
	CreateAlertChannel(context.Context, *tables.TableAlertChannel) error
	UpdateAlertChannel(context.Context, *tables.TableAlertChannel) error
	DeleteAlertChannel(context.Context, string) error

	ListAlertRules(context.Context) ([]tables.TableAlertRule, error)
	GetAlertRule(context.Context, string) (*tables.TableAlertRule, error)
	CreateAlertRule(context.Context, *tables.TableAlertRule) error
	UpdateAlertRule(context.Context, *tables.TableAlertRule) error
	DeleteAlertRule(context.Context, string) error

	ListAlertCooldowns(context.Context) ([]tables.TableAlertCooldown, error)
	UpsertAlertCooldown(context.Context, string, time.Time) error
}

func (s *RDBConfigStore) ListAlertCooldowns(ctx context.Context) ([]tables.TableAlertCooldown, error) {
	var cooldowns []tables.TableAlertCooldown
	err := s.ScopedDB(ctx).Find(&cooldowns).Error
	return cooldowns, err
}

func (s *RDBConfigStore) UpsertAlertCooldown(ctx context.Context, key string, lastSentAt time.Time) error {
	row := tables.TableAlertCooldown{Key: key, LastSentAt: lastSentAt.UTC()}
	return s.DB().WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"last_sent_at", "updated_at"}),
	}).Create(&row).Error
}

func (s *RDBConfigStore) ListAlertChannels(ctx context.Context) ([]tables.TableAlertChannel, error) {
	var channels []tables.TableAlertChannel
	err := s.ScopedDB(ctx).Order("created_at DESC").Find(&channels).Error
	return channels, err
}

func (s *RDBConfigStore) GetAlertChannel(ctx context.Context, id string) (*tables.TableAlertChannel, error) {
	var channel tables.TableAlertChannel
	if err := s.ScopedDB(ctx).First(&channel, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &channel, nil
}

func (s *RDBConfigStore) CreateAlertChannel(ctx context.Context, channel *tables.TableAlertChannel) error {
	return s.DB().WithContext(ctx).Create(channel).Error
}

func (s *RDBConfigStore) UpdateAlertChannel(ctx context.Context, channel *tables.TableAlertChannel) error {
	result := s.DB().WithContext(ctx).Save(channel)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *RDBConfigStore) DeleteAlertChannel(ctx context.Context, id string) error {
	return s.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rules []tables.TableAlertRule
		if err := tx.Find(&rules).Error; err != nil {
			return err
		}
		for i := range rules {
			if err := rules[i].AfterFind(tx); err != nil {
				return err
			}
			filtered := rules[i].ChannelIDs[:0]
			for _, channelID := range rules[i].ChannelIDs {
				if channelID != id {
					filtered = append(filtered, channelID)
				}
			}
			if len(filtered) != len(rules[i].ChannelIDs) {
				rules[i].ChannelIDs = filtered
				if len(filtered) == 0 {
					rules[i].Enabled = false
				}
				if err := tx.Save(&rules[i]).Error; err != nil {
					return err
				}
			}
		}
		result := tx.Delete(&tables.TableAlertChannel{}, "id = ?", id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrNotFound
		}
		return nil
	})
}

func (s *RDBConfigStore) ListAlertRules(ctx context.Context) ([]tables.TableAlertRule, error) {
	var rules []tables.TableAlertRule
	err := s.ScopedDB(ctx).Order("created_at DESC").Find(&rules).Error
	return rules, err
}

func (s *RDBConfigStore) GetAlertRule(ctx context.Context, id string) (*tables.TableAlertRule, error) {
	var rule tables.TableAlertRule
	if err := s.ScopedDB(ctx).First(&rule, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &rule, nil
}

func (s *RDBConfigStore) CreateAlertRule(ctx context.Context, rule *tables.TableAlertRule) error {
	return s.DB().WithContext(ctx).Create(rule).Error
}

func (s *RDBConfigStore) UpdateAlertRule(ctx context.Context, rule *tables.TableAlertRule) error {
	result := s.DB().WithContext(ctx).Save(rule)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *RDBConfigStore) DeleteAlertRule(ctx context.Context, id string) error {
	result := s.DB().WithContext(ctx).Delete(&tables.TableAlertRule{}, "id = ?", id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
