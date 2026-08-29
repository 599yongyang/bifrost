package logstore

import (
	"database/sql/driver"
	"time"

	"github.com/bytedance/sonic"
	"gorm.io/gorm"
)

type DailyReportAudience string

const (
	DailyReportAudienceInternal DailyReportAudience = "internal"
	DailyReportAudienceExternal DailyReportAudience = "external"
)

func (a DailyReportAudience) Value() (driver.Value, error) {
	return string(a), nil
}

type DailyReportDeliveryStatus string

const (
	DailyReportDeliveryStatusDelivered DailyReportDeliveryStatus = "delivered"
	DailyReportDeliveryStatusFailed    DailyReportDeliveryStatus = "failed"
)

func (s DailyReportDeliveryStatus) Value() (driver.Value, error) {
	return string(s), nil
}

type DailyReportRunStatus string

const (
	DailyReportRunStatusRunning  DailyReportRunStatus = "running"
	DailyReportRunStatusPrepared DailyReportRunStatus = "prepared"
	DailyReportRunStatusSuccess  DailyReportRunStatus = "success"
	DailyReportRunStatusPartial  DailyReportRunStatus = "partial"
	DailyReportRunStatusFailed   DailyReportRunStatus = "failed"
)

func (s DailyReportRunStatus) Value() (driver.Value, error) {
	return string(s), nil
}

type DailyReportAudienceStatus string

const (
	DailyReportAudienceStatusPending    DailyReportAudienceStatus = "pending"
	DailyReportAudienceStatusDelivered  DailyReportAudienceStatus = "delivered"
	DailyReportAudienceStatusFailed     DailyReportAudienceStatus = "failed"
	DailyReportAudienceStatusNotEnabled DailyReportAudienceStatus = "not_enabled"
	DailyReportAudienceStatusNoChannels DailyReportAudienceStatus = "no_channels"
)

func (s DailyReportAudienceStatus) Value() (driver.Value, error) {
	return string(s), nil
}

type DailyReportErrorBucket struct {
	Key         string  `json:"key"`
	Label       string  `json:"label"`
	Count       int64   `json:"count"`
	Rate        float64 `json:"rate"`
	Description string  `json:"description,omitempty"`
}

type DailyReportTrendValue struct {
	Current         float64 `json:"current"`
	Previous        float64 `json:"previous"`
	Delta           float64 `json:"delta"`
	DeltaPercentage float64 `json:"delta_percentage"`
}

type DailyReportOverview struct {
	UserRequests         int64                    `json:"user_requests"`
	ProviderAttempts     int64                    `json:"provider_attempts"`
	SystemSuccessRate    float64                  `json:"system_success_rate"`
	UserSuccessRate      float64                  `json:"user_success_rate"`
	FallbackRecoveries   int64                    `json:"fallback_recoveries"`
	FallbackRecoveryRate float64                  `json:"fallback_recovery_rate"`
	RetryCount           int64                    `json:"retry_count"`
	SlowRequests         int64                    `json:"slow_requests"`
	SlowRequestRate      float64                  `json:"slow_request_rate"`
	AverageLatencyMs     float64                  `json:"average_latency_ms"`
	P95LatencyMs         float64                  `json:"p95_latency_ms"`
	P99LatencyMs         float64                  `json:"p99_latency_ms"`
	ErrorBuckets         []DailyReportErrorBucket `json:"error_buckets,omitempty"`
}

type DailyProviderReportRow struct {
	Provider         string                   `json:"provider"`
	Attempts         int64                    `json:"attempts"`
	SuccessCount     int64                    `json:"success_count"`
	SuccessRate      float64                  `json:"success_rate"`
	RetryCount       int64                    `json:"retry_count"`
	SlowRequests     int64                    `json:"slow_requests"`
	SlowRequestRate  float64                  `json:"slow_request_rate"`
	AverageLatencyMs float64                  `json:"average_latency_ms"`
	P95LatencyMs     float64                  `json:"p95_latency_ms"`
	P99LatencyMs     float64                  `json:"p99_latency_ms"`
	ErrorBuckets     []DailyReportErrorBucket `json:"error_buckets,omitempty"`
	Models           []DailyModelReportRow    `json:"models,omitempty"`
}

type DailyModelReportRow struct {
	Provider         string                   `json:"provider"`
	Model            string                   `json:"model"`
	Attempts         int64                    `json:"attempts"`
	SuccessCount     int64                    `json:"success_count"`
	SuccessRate      float64                  `json:"success_rate"`
	RetryCount       int64                    `json:"retry_count"`
	SlowRequests     int64                    `json:"slow_requests"`
	SlowRequestRate  float64                  `json:"slow_request_rate"`
	AverageLatencyMs float64                  `json:"average_latency_ms"`
	P95LatencyMs     float64                  `json:"p95_latency_ms"`
	P99LatencyMs     float64                  `json:"p99_latency_ms"`
	ErrorBuckets     []DailyReportErrorBucket `json:"error_buckets,omitempty"`
}

type DailyReportTrends struct {
	UserRequests       DailyReportTrendValue `json:"user_requests"`
	UserSuccessRate    DailyReportTrendValue `json:"user_success_rate"`
	SystemSuccessRate  DailyReportTrendValue `json:"system_success_rate"`
	FallbackRecoveries DailyReportTrendValue `json:"fallback_recoveries"`
	SlowRequestRate    DailyReportTrendValue `json:"slow_request_rate"`
	AverageLatencyMs   DailyReportTrendValue `json:"average_latency_ms"`
	P95LatencyMs       DailyReportTrendValue `json:"p95_latency_ms"`
	P99LatencyMs       DailyReportTrendValue `json:"p99_latency_ms"`
}

// PublicDailyReportTrends is an explicit allowlist for customer-facing output.
// It intentionally omits provider-attempt/system success metrics.
type PublicDailyReportTrends struct {
	UserRequests       DailyReportTrendValue `json:"user_requests"`
	UserSuccessRate    DailyReportTrendValue `json:"user_success_rate"`
	FallbackRecoveries DailyReportTrendValue `json:"fallback_recoveries"`
	SlowRequestRate    DailyReportTrendValue `json:"slow_request_rate"`
	AverageLatencyMs   DailyReportTrendValue `json:"average_latency_ms"`
	P95LatencyMs       DailyReportTrendValue `json:"p95_latency_ms"`
	P99LatencyMs       DailyReportTrendValue `json:"p99_latency_ms"`
}

type DailyReportSnapshot struct {
	BusinessDate    string                   `json:"business_date"`
	Timezone        string                   `json:"timezone"`
	WindowStart     time.Time                `json:"window_start"`
	WindowEnd       time.Time                `json:"window_end"`
	GeneratedAt     time.Time                `json:"generated_at"`
	SlowThresholdMs int64                    `json:"slow_threshold_ms"`
	Overview        DailyReportOverview      `json:"overview"`
	Providers       []DailyProviderReportRow `json:"providers"`
	Trends          DailyReportTrends        `json:"trends"`
}

type PublicDailyReportSnapshot struct {
	BusinessDate         string                   `json:"business_date"`
	Timezone             string                   `json:"timezone"`
	WindowStart          time.Time                `json:"window_start"`
	WindowEnd            time.Time                `json:"window_end"`
	GeneratedAt          time.Time                `json:"generated_at"`
	SlowThresholdMs      int64                    `json:"slow_threshold_ms"`
	UserRequests         int64                    `json:"user_requests"`
	UserSuccessRate      float64                  `json:"user_success_rate"`
	FallbackRecoveries   int64                    `json:"fallback_recoveries"`
	FallbackRecoveryRate float64                  `json:"fallback_recovery_rate"`
	SlowRequests         int64                    `json:"slow_requests"`
	SlowRequestRate      float64                  `json:"slow_request_rate"`
	AverageLatencyMs     float64                  `json:"average_latency_ms"`
	P95LatencyMs         float64                  `json:"p95_latency_ms"`
	P99LatencyMs         float64                  `json:"p99_latency_ms"`
	ErrorBuckets         []DailyReportErrorBucket `json:"error_buckets,omitempty"`
	Trends               PublicDailyReportTrends  `json:"trends"`
}

func (s DailyReportSnapshot) PublicView() PublicDailyReportSnapshot {
	return PublicDailyReportSnapshot{
		BusinessDate:         s.BusinessDate,
		Timezone:             s.Timezone,
		WindowStart:          s.WindowStart,
		WindowEnd:            s.WindowEnd,
		GeneratedAt:          s.GeneratedAt,
		SlowThresholdMs:      s.SlowThresholdMs,
		UserRequests:         s.Overview.UserRequests,
		UserSuccessRate:      s.Overview.UserSuccessRate,
		FallbackRecoveries:   s.Overview.FallbackRecoveries,
		FallbackRecoveryRate: s.Overview.FallbackRecoveryRate,
		SlowRequests:         s.Overview.SlowRequests,
		SlowRequestRate:      s.Overview.SlowRequestRate,
		AverageLatencyMs:     s.Overview.AverageLatencyMs,
		P95LatencyMs:         s.Overview.P95LatencyMs,
		P99LatencyMs:         s.Overview.P99LatencyMs,
		ErrorBuckets:         s.Overview.ErrorBuckets,
		Trends: PublicDailyReportTrends{
			UserRequests:       s.Trends.UserRequests,
			UserSuccessRate:    s.Trends.UserSuccessRate,
			FallbackRecoveries: s.Trends.FallbackRecoveries,
			SlowRequestRate:    s.Trends.SlowRequestRate,
			AverageLatencyMs:   s.Trends.AverageLatencyMs,
			P95LatencyMs:       s.Trends.P95LatencyMs,
			P99LatencyMs:       s.Trends.P99LatencyMs,
		},
	}
}

type DailyReportRun struct {
	ID                     string                    `gorm:"type:varchar(36);primaryKey" json:"id"`
	BusinessDate           string                    `gorm:"type:varchar(10);index:idx_daily_report_runs_business_date_tz,unique;not null" json:"business_date"`
	Timezone               string                    `gorm:"type:varchar(128);index:idx_daily_report_runs_business_date_tz,unique;not null" json:"timezone"`
	WindowStart            time.Time                 `gorm:"index;not null" json:"window_start"`
	WindowEnd              time.Time                 `gorm:"index;not null" json:"window_end"`
	SlowThresholdMs        int64                     `gorm:"not null" json:"slow_threshold_ms"`
	Trigger                string                    `gorm:"type:varchar(32);index;not null" json:"trigger"`
	Status                 DailyReportRunStatus      `gorm:"type:varchar(16);index;not null;default:'running'" json:"status"`
	InternalStatus         DailyReportAudienceStatus `gorm:"type:varchar(16);index;not null;default:'pending'" json:"internal_status"`
	ExternalStatus         DailyReportAudienceStatus `gorm:"type:varchar(16);index;not null;default:'pending'" json:"external_status"`
	InternalStatusDetail   string                    `gorm:"type:text" json:"internal_status_detail,omitempty"`
	ExternalStatusDetail   string                    `gorm:"type:text" json:"external_status_detail,omitempty"`
	GeneratedAt            time.Time                 `gorm:"index;not null" json:"generated_at"`
	SnapshotJSON           string                    `gorm:"type:text;not null" json:"-"`
	InternalContent        string                    `gorm:"type:text" json:"internal_content"`
	ExternalContent        string                    `gorm:"type:text" json:"external_content"`
	InternalChannelIDsJSON string                    `gorm:"type:text;not null" json:"-"`
	ExternalChannelIDsJSON string                    `gorm:"type:text;not null" json:"-"`
	StartedAt              time.Time                 `gorm:"index;not null" json:"started_at"`
	CompletedAt            *time.Time                `gorm:"index" json:"completed_at,omitempty"`
	CreatedAt              time.Time                 `gorm:"index;not null" json:"created_at"`

	Snapshot           DailyReportSnapshot `gorm:"-" json:"snapshot"`
	InternalChannelIDs []string            `gorm:"-" json:"internal_channel_ids"`
	ExternalChannelIDs []string            `gorm:"-" json:"external_channel_ids"`
}

func (DailyReportRun) TableName() string { return "daily_report_runs" }

func (r *DailyReportRun) BeforeSave(*gorm.DB) error {
	snapshot, err := sonic.Marshal(r.Snapshot)
	if err != nil {
		return err
	}
	internal, err := sonic.Marshal(r.InternalChannelIDs)
	if err != nil {
		return err
	}
	external, err := sonic.Marshal(r.ExternalChannelIDs)
	if err != nil {
		return err
	}
	r.SnapshotJSON = string(snapshot)
	r.InternalChannelIDsJSON = string(internal)
	r.ExternalChannelIDsJSON = string(external)
	return nil
}

func (r *DailyReportRun) UpdateMap() (map[string]any, error) {
	if err := r.BeforeSave(nil); err != nil {
		return nil, err
	}
	return map[string]any{
		"business_date":             r.BusinessDate,
		"timezone":                  r.Timezone,
		"window_start":              r.WindowStart,
		"window_end":                r.WindowEnd,
		"slow_threshold_ms":         r.SlowThresholdMs,
		"trigger":                   r.Trigger,
		"status":                    r.Status,
		"internal_status":           r.InternalStatus,
		"external_status":           r.ExternalStatus,
		"internal_status_detail":    r.InternalStatusDetail,
		"external_status_detail":    r.ExternalStatusDetail,
		"generated_at":              r.GeneratedAt,
		"snapshot_json":             r.SnapshotJSON,
		"internal_content":          r.InternalContent,
		"external_content":          r.ExternalContent,
		"internal_channel_ids_json": r.InternalChannelIDsJSON,
		"external_channel_ids_json": r.ExternalChannelIDsJSON,
		"started_at":                r.StartedAt,
		"completed_at":              r.CompletedAt,
		"created_at":                r.CreatedAt,
	}, nil
}

func (r *DailyReportRun) AfterFind(*gorm.DB) error {
	if r.SnapshotJSON != "" {
		if err := sonic.Unmarshal([]byte(r.SnapshotJSON), &r.Snapshot); err != nil {
			return err
		}
	}
	if r.InternalChannelIDsJSON == "" {
		r.InternalChannelIDs = []string{}
	} else if err := sonic.Unmarshal([]byte(r.InternalChannelIDsJSON), &r.InternalChannelIDs); err != nil {
		return err
	}
	if r.ExternalChannelIDsJSON == "" {
		r.ExternalChannelIDs = []string{}
		return nil
	}
	return sonic.Unmarshal([]byte(r.ExternalChannelIDsJSON), &r.ExternalChannelIDs)
}

type DailyReportDelivery struct {
	ID           string                    `gorm:"type:varchar(36);primaryKey" json:"id"`
	RunID        string                    `gorm:"type:varchar(36);index;not null" json:"run_id"`
	Audience     DailyReportAudience       `gorm:"type:varchar(16);index;not null" json:"audience"`
	ChannelID    string                    `gorm:"type:varchar(255);index;not null" json:"channel_id"`
	ChannelName  string                    `gorm:"type:varchar(255)" json:"channel_name"`
	ChannelType  string                    `gorm:"type:varchar(32);index" json:"channel_type"`
	AttemptNo    int                       `gorm:"not null;default:1" json:"attempt_no"`
	Status       DailyReportDeliveryStatus `gorm:"type:varchar(16);index;not null" json:"status"`
	StatusDetail string                    `gorm:"type:text" json:"status_detail,omitempty"`
	CreatedAt    time.Time                 `gorm:"index;not null" json:"created_at"`
}

func (DailyReportDelivery) TableName() string { return "daily_report_deliveries" }

type DailyReportHistoryQuery struct {
	Limit     int
	Offset    int
	Audiences []DailyReportAudience
}

type DailyReportMetricsQuery struct {
	BusinessDate    string
	Timezone        string
	WindowStart     time.Time
	WindowEnd       time.Time
	SlowThresholdMs int64
	GeneratedAt     time.Time
	Progress        func(DailyReportMetricsProgress)
}

type DailyReportMetricsProgress struct {
	Stage     string `json:"stage"`
	Processed int64  `json:"processed"`
}
