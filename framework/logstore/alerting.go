package logstore

import (
	"context"
	"time"
)

func (s *RDBLogStore) CreateAlertHistory(ctx context.Context, history *AlertHistory) error {
	return s.db.WithContext(ctx).Create(history).Error
}

func (s *RDBLogStore) ListAlertHistory(ctx context.Context, params AlertHistoryQuery) ([]AlertHistory, int64, error) {
	query := s.db.WithContext(ctx).Model(&AlertHistory{})
	if len(params.Statuses) > 0 {
		query = query.Where("status IN ?", params.Statuses)
	}
	if len(params.ScopeTypes) > 0 {
		query = query.Where("scope_type IN ?", params.ScopeTypes)
	}
	if len(params.ChannelTypes) > 0 {
		query = query.Where("channel_type IN ?", params.ChannelTypes)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	limit := params.Limit
	if limit <= 0 || limit > 1000 {
		limit = 25
	}
	offset := params.Offset
	if offset < 0 {
		offset = 0
	}
	var history []AlertHistory
	err := query.Order("created_at DESC").Limit(limit).Offset(offset).Find(&history).Error
	return history, total, err
}

func (s *RDBLogStore) DeleteAlertHistoryBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	result := s.db.WithContext(ctx).Where("created_at < ?", cutoff).Delete(&AlertHistory{})
	return result.RowsAffected, result.Error
}

func (s *RDBLogStore) ListLatestAlertRuleSends(ctx context.Context) ([]AlertHistory, error) {
	var rows []AlertHistory
	if s.db.Dialector.Name() != "sqlite" {
		err := s.db.WithContext(ctx).Model(&AlertHistory{}).
			Select("rule_id, scope_type, scope_id, target_type, target_id, MAX(created_at) AS created_at").
			Where("status = ?", "sent").
			Group("rule_id, scope_type, scope_id, target_type, target_id").
			Scan(&rows).Error
		return rows, err
	}
	err := s.db.WithContext(ctx).Table("enterprise_alert_history AS h").Select("h.*").
		Where(`h.status = ? AND NOT EXISTS (
			SELECT 1 FROM enterprise_alert_history newer
			WHERE newer.status = ?
			  AND newer.rule_id = h.rule_id
			  AND newer.scope_type = h.scope_type
			  AND newer.scope_id = h.scope_id
			  AND newer.target_type = h.target_type
			  AND newer.target_id = h.target_id
			  AND newer.created_at > h.created_at
		)`, "sent", "sent").Find(&rows).Error
	return rows, err
}

func (h *HybridLogStore) CreateAlertHistory(ctx context.Context, history *AlertHistory) error {
	return h.inner.CreateAlertHistory(ctx, history)
}

func (h *HybridLogStore) ListAlertHistory(ctx context.Context, query AlertHistoryQuery) ([]AlertHistory, int64, error) {
	return h.inner.ListAlertHistory(ctx, query)
}

func (h *HybridLogStore) DeleteAlertHistoryBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	return h.inner.DeleteAlertHistoryBefore(ctx, cutoff)
}

func (h *HybridLogStore) ListLatestAlertRuleSends(ctx context.Context) ([]AlertHistory, error) {
	return h.inner.ListLatestAlertRuleSends(ctx)
}
