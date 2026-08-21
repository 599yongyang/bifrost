package logstore

import (
	"context"
	"errors"
	"time"
)

const (
	ObservationExportStatusPending     = "pending"
	ObservationExportStatusExported    = "exported"
	ObservationExportStatusNotExported = "not_exported"
	ObservationExportStatusFailed      = "failed"
	ObservationExportStatusUnavailable = "unavailable"

	ObservationExportSourceAutomatic = "automatic"
	ObservationExportSourceManual    = "manual"
)

var ErrObservationExportUnsupported = errors.New("observability export status is not supported by this log store")

// ObservationExport records one log's export state for one configured observability target.
// FailureReason is a bounded, client-safe code; raw transport errors remain server-log only.
type ObservationExport struct {
	LogID           string     `gorm:"primaryKey;type:varchar(255);index" json:"log_id"`
	TargetID        string     `gorm:"primaryKey;type:varchar(64)" json:"target_id"`
	Status          string     `gorm:"type:varchar(32);index;not null" json:"status"`
	Source          string     `gorm:"type:varchar(16);not null" json:"source"`
	Reason          string     `gorm:"type:varchar(64)" json:"reason,omitempty"`
	SelectionRule   string     `gorm:"type:varchar(255)" json:"selection_rule,omitempty"`
	ExternalTraceID string     `gorm:"type:varchar(64)" json:"external_trace_id,omitempty"`
	Attempts        int        `gorm:"not null;default:0" json:"attempts"`
	ExportedAt      *time.Time `json:"exported_at,omitempty"`
	CreatedAt       time.Time  `gorm:"not null" json:"created_at"`
	UpdatedAt       time.Time  `gorm:"not null" json:"updated_at"`
}

func (ObservationExport) TableName() string { return "observability_exports" }

// ObservationExportStore is an optional seam implemented by durable log stores.
// It intentionally stays outside LogStore so lightweight/test adapters are not forced
// to implement a feature they do not use.
type ObservationExportStore interface {
	UpsertObservationExport(ctx context.Context, state *ObservationExport) error
	BatchUpsertObservationExports(ctx context.Context, states []ObservationExport) error
	GetObservationExports(ctx context.Context, logIDs []string) ([]ObservationExport, error)
}

// LogAccessStore performs a visibility-scoped, metadata-only lookup for mutation
// authorization without hydrating object-storage payloads.
type LogAccessStore interface {
	FindLogIDForAccess(ctx context.Context, id string) error
}
