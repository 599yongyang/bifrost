package logstore

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/framework/queryscope"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestObservationExportStoreUpsertAndDeleteWithLog(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:observation-export?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&Log{}, &ObservationExport{}); err != nil {
		t.Fatal(err)
	}
	store := &RDBLogStore{db: db}
	entry := &Log{ID: "log-1", Timestamp: time.Now(), Object: "image_generation", Provider: "openai", Model: "image", Status: "success", CreatedAt: time.Now()}
	if err := db.Create(entry).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.FindLogIDForAccess(context.Background(), entry.ID); err != nil {
		t.Fatalf("authorized ID projection failed: %v", err)
	}
	deniedCtx := queryscope.WithQueryScope(context.Background(), func(db *gorm.DB) *gorm.DB { return db.Where("1 = 0") })
	if err := store.FindLogIDForAccess(deniedCtx, entry.ID); err != ErrNotFound {
		t.Fatalf("scoped ID projection error = %v, want ErrNotFound", err)
	}
	state := &ObservationExport{LogID: entry.ID, TargetID: "profile-0", Status: ObservationExportStatusPending, Source: ObservationExportSourceManual, Attempts: 1}
	if err := store.UpsertObservationExport(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	state.Status = ObservationExportStatusExported
	state.Reason = "manual"
	state.Attempts = 2
	if err := store.BatchUpsertObservationExports(context.Background(), []ObservationExport{*state}); err != nil {
		t.Fatal(err)
	}
	states, err := store.GetObservationExports(context.Background(), []string{entry.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || states[0].Status != ObservationExportStatusExported || states[0].Attempts != 2 {
		t.Fatalf("states = %+v", states)
	}
	if err := store.DeleteLog(context.Background(), entry.ID); err != nil {
		t.Fatal(err)
	}
	states, err = store.GetObservationExports(context.Background(), []string{entry.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 0 {
		t.Fatalf("observation export state survived log deletion: %+v", states)
	}
}

func TestMigrationCreateObservationExportsTableIsIdempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:observation-export-migration?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := migrationCreateObservationExportsTable(context.Background(), db, testLogger{}); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasTable(&ObservationExport{}) {
		t.Fatal("observability_exports table was not created")
	}
	if err := migrationCreateObservationExportsTable(context.Background(), db, testLogger{}); err != nil {
		t.Fatalf("idempotent migration failed: %v", err)
	}
}

func TestObservationExportOlderWritesCannotOverwriteNewerState(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:observation-export-ordering?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&ObservationExport{}); err != nil {
		t.Fatal(err)
	}
	store := &RDBLogStore{db: db}
	newerAt := time.Now().UTC()
	newer := ObservationExport{LogID: "ordered", TargetID: "target", Status: ObservationExportStatusExported, Source: ObservationExportSourceAutomatic, Reason: "newer", UpdatedAt: newerAt}
	older := newer
	older.Status = ObservationExportStatusPending
	older.Reason = "older"
	older.UpdatedAt = newerAt.Add(-time.Minute)
	if err := store.UpsertObservationExport(context.Background(), &newer); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertObservationExport(context.Background(), &older); err != nil {
		t.Fatal(err)
	}
	if err := store.BatchUpsertObservationExports(context.Background(), []ObservationExport{older}); err != nil {
		t.Fatal(err)
	}
	states, err := store.GetObservationExports(context.Background(), []string{"ordered"})
	if err != nil || len(states) != 1 || states[0].Status != ObservationExportStatusExported || states[0].Reason != "newer" {
		t.Fatalf("ordered state = %+v err=%v", states, err)
	}
}
