package logstore

import (
	"context"
	"testing"
	"time"
)

type panickingRetentionManager struct{}

func (panickingRetentionManager) DeleteLogsBatch(context.Context, time.Time, int) (int64, error) {
	panic("storage driver panic")
}

func TestCleanupOldLogsSafelyContainsStoragePanic(t *testing.T) {
	cleaner := NewLogsCleaner(panickingRetentionManager{}, CleanerConfig{RetentionDays: 1}, testLogger{})
	cleaner.cleanupOldLogsSafely(context.Background())
}
