package alerting

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/logstore"
)

const (
	DailyReportJobKind                = "daily_report.generate"
	defaultDailyReportTimezone        = "Asia/Shanghai"
	defaultDailyReportGenerateTime    = "03:00"
	defaultDailyReportSendTime        = "09:00"
	defaultDailyReportSlowThresholdMs = int64(10000)
	dailyReportGenerationGracePeriod  = 2 * time.Hour
	dailyReportMaxQueryDuration       = 2 * time.Hour
)

var ErrDailyReportGenerationInProgress = errors.New("this daily report is already being generated")
var ErrDailyReportQueryInProgress = errors.New("another daily report query is already running")

type dailyReportReleaseLockStore interface {
	ReleaseLock(context.Context, string, string) (bool, error)
}

type DailyReportPreview struct {
	BusinessDate    string                          `json:"business_date"`
	Settings        tables.TableDailyReportSettings `json:"settings"`
	Snapshot        logstore.DailyReportSnapshot    `json:"snapshot"`
	InternalContent string                          `json:"internal_content"`
	ExternalContent string                          `json:"external_content"`
}

type DailyReportRunDetail struct {
	Run                   logstore.DailyReportRun            `json:"run"`
	Deliveries            []logstore.DailyReportDelivery     `json:"deliveries"`
	CurrentStatus         logstore.DailyReportRunStatus      `json:"current_status"`
	CurrentInternalStatus logstore.DailyReportAudienceStatus `json:"current_internal_status"`
	CurrentExternalStatus logstore.DailyReportAudienceStatus `json:"current_external_status"`
}

type DailyReportGenerateResult struct {
	Run        logstore.DailyReportRun        `json:"run"`
	Deliveries []logstore.DailyReportDelivery `json:"deliveries"`
	Created    bool                           `json:"created"`
}

type DailyReportJobMeta struct {
	BusinessDate string                          `json:"business_date"`
	Deliver      bool                            `json:"deliver"`
	Fingerprint  string                          `json:"fingerprint"`
	Settings     tables.TableDailyReportSettings `json:"settings"`
	Stage        string                          `json:"stage"`
	Processed    int64                           `json:"processed"`
	Percent      int                             `json:"percent"`
	RunID        string                          `json:"run_id,omitempty"`
	Message      string                          `json:"message,omitempty"`
}

func (m *Manager) GetDailyReportSettings(ctx context.Context) (*tables.TableDailyReportSettings, error) {
	settings, err := m.store.GetDailyReportSettings(ctx)
	if errors.Is(err, configstore.ErrNotFound) {
		return defaultDailyReportSettings(), nil
	}
	if err == nil {
		applyDailyReportDefaults(settings)
	}
	return settings, err
}

func (m *Manager) UpdateDailyReportSettings(ctx context.Context, settings *tables.TableDailyReportSettings) (*tables.TableDailyReportSettings, error) {
	if settings == nil {
		return nil, fmt.Errorf("daily report settings are required")
	}
	normalized := cloneDailyReportSettings(settings)
	applyDailyReportDefaults(normalized)
	if err := m.validateDailyReportSettings(ctx, normalized); err != nil {
		return nil, err
	}
	if err := m.store.UpsertDailyReportSettings(ctx, normalized); err != nil {
		return nil, err
	}
	return m.GetDailyReportSettings(ctx)
}

func (m *Manager) PreviewDailyReport(ctx context.Context, settings *tables.TableDailyReportSettings, businessDate string) (*DailyReportPreview, error) {
	persistedSettings, err := m.GetDailyReportSettings(ctx)
	if err != nil {
		return nil, err
	}
	activeSettings := settings
	if activeSettings == nil {
		activeSettings = persistedSettings
	}
	activeSettings = cloneDailyReportSettings(activeSettings)
	applyDailyReportDefaults(activeSettings)
	// Preview only needs a valid reporting window. It must remain usable before
	// channels are selected so operators can review both audiences safely.
	if err := activeSettings.Validate(); err != nil {
		return nil, err
	}
	resolvedDate, err := resolveDailyReportBusinessDate(activeSettings, businessDate, m.currentTime())
	if err != nil {
		return nil, err
	}
	if m.dailyReports != nil {
		existing, findErr := m.dailyReports.FindDailyReportRunByBusinessDate(ctx, resolvedDate, activeSettings.Timezone)
		if findErr == nil && (existing.InternalContent != "" || existing.ExternalContent != "") {
			return &DailyReportPreview{
				BusinessDate:    existing.BusinessDate,
				Settings:        *activeSettings,
				Snapshot:        existing.Snapshot,
				InternalContent: existing.InternalContent,
				ExternalContent: existing.ExternalContent,
			}, nil
		}
		if findErr != nil && !errors.Is(findErr, logstore.ErrNotFound) {
			return nil, findErr
		}
	}
	snapshot, err := m.buildDailyReportSnapshot(ctx, activeSettings, resolvedDate)
	if err != nil {
		return nil, err
	}
	return &DailyReportPreview{
		BusinessDate:    snapshot.BusinessDate,
		Settings:        *activeSettings,
		Snapshot:        *snapshot,
		InternalContent: renderInternalDailyReport(*snapshot),
		ExternalContent: renderExternalDailyReport(snapshot.PublicView()),
	}, nil
}

func NewDailyReportJobMetadata(
	businessDate string,
	deliver bool,
	settings *tables.TableDailyReportSettings,
) (metadata string, jobID string, err error) {
	if settings == nil {
		return "", "", fmt.Errorf("daily report settings are required")
	}
	settingsSnapshot := cloneDailyReportSettings(settings)
	applyDailyReportDefaults(settingsSnapshot)
	if err := settingsSnapshot.Validate(); err != nil {
		return "", "", err
	}
	resolvedDate, err := resolveDailyReportBusinessDate(settingsSnapshot, strings.TrimSpace(businessDate), time.Now())
	if err != nil {
		return "", "", err
	}
	fingerprintInput, err := sonic.Marshal(struct {
		BusinessDate       string   `json:"business_date"`
		Deliver            bool     `json:"deliver"`
		Enabled            bool     `json:"enabled"`
		Timezone           string   `json:"timezone"`
		GenerateTime       string   `json:"generate_time"`
		SendTime           string   `json:"send_time"`
		SlowThresholdMs    int64    `json:"slow_threshold_ms"`
		InternalEnabled    bool     `json:"internal_enabled"`
		InternalChannelIDs []string `json:"internal_channel_ids"`
		ExternalEnabled    bool     `json:"external_enabled"`
		ExternalChannelIDs []string `json:"external_channel_ids"`
	}{
		BusinessDate:       resolvedDate,
		Deliver:            deliver,
		Enabled:            settingsSnapshot.Enabled,
		Timezone:           settingsSnapshot.Timezone,
		GenerateTime:       settingsSnapshot.GenerateTime,
		SendTime:           settingsSnapshot.SendTime,
		SlowThresholdMs:    settingsSnapshot.SlowThresholdMs,
		InternalEnabled:    settingsSnapshot.InternalEnabled,
		InternalChannelIDs: settingsSnapshot.InternalChannelIDs,
		ExternalEnabled:    settingsSnapshot.ExternalEnabled,
		ExternalChannelIDs: settingsSnapshot.ExternalChannelIDs,
	})
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256(fingerprintInput)
	fingerprint := hex.EncodeToString(digest[:])
	encoded, err := sonic.Marshal(DailyReportJobMeta{
		BusinessDate: resolvedDate,
		Deliver:      deliver,
		Fingerprint:  fingerprint,
		Settings:     *settingsSnapshot,
		Stage:        "pending",
		Percent:      0,
		Message:      "Waiting to start",
	})
	if err != nil {
		return "", "", err
	}
	return string(encoded), uuid.NewSHA1(uuid.NameSpaceURL, []byte("bifrost:daily-report-job:"+fingerprint)).String(), nil
}

func (m *Manager) RunDailyReportJob(
	ctx context.Context,
	metadata string,
	updateProgress func(string) error,
) (string, error) {
	var meta DailyReportJobMeta
	if err := sonic.Unmarshal([]byte(metadata), &meta); err != nil {
		return "", fmt.Errorf("invalid daily report job metadata: %w", err)
	}
	settings := cloneDailyReportSettings(&meta.Settings)
	applyDailyReportDefaults(settings)
	resolvedDate, err := resolveDailyReportBusinessDate(settings, meta.BusinessDate, m.currentTime())
	if err != nil {
		return "", err
	}
	meta.BusinessDate = resolvedDate
	lastProgressAt := time.Time{}
	reportProgress := func(stage string, processed int64, percent int, message string, force bool) {
		meta.Stage = stage
		meta.Processed = processed
		meta.Percent = percent
		meta.Message = message
		if updateProgress == nil || (!force && time.Since(lastProgressAt) < time.Second) {
			return
		}
		lastProgressAt = time.Now()
		if encoded, marshalErr := sonic.Marshal(meta); marshalErr == nil {
			_ = updateProgress(string(encoded))
		}
	}
	reportProgress("preparing", 0, 5, "Preparing report query", true)
	metricsProgress := func(progress logstore.DailyReportMetricsProgress) {
		switch progress.Stage {
		case "scanning_logs":
			reportProgress(progress.Stage, progress.Processed, 55, "Scanning requests and fallback chains", false)
		case "building_report":
			reportProgress(progress.Stage, progress.Processed, 90, "Building report content", true)
		}
	}
	result, err := m.generateDailyReportWithProgress(ctx, settings, resolvedDate, "manual", meta.Deliver, metricsProgress)
	if err != nil {
		return "", err
	}
	if meta.Deliver && result.Run.Status == logstore.DailyReportRunStatusPrepared {
		deliveries, deliveredRun, deliverErr := m.deliverPreparedDailyReport(ctx, result.Run.ID)
		if deliverErr != nil {
			return "", deliverErr
		}
		result.Deliveries = deliveries
		result.Run = *deliveredRun
	}
	meta.RunID = result.Run.ID
	reportProgress("completed", meta.Processed, 100, "Report ready", true)
	encoded, err := sonic.Marshal(meta)
	return string(encoded), err
}

func (m *Manager) GenerateDailyReportNow(ctx context.Context, businessDate string) (*DailyReportGenerateResult, error) {
	settings, err := m.GetDailyReportSettings(ctx)
	if err != nil {
		return nil, err
	}
	resolvedDate, err := resolveDailyReportBusinessDate(settings, businessDate, m.currentTime())
	if err != nil {
		return nil, err
	}
	if m.dailyReports != nil {
		existing, findErr := m.dailyReports.FindDailyReportRunByBusinessDate(ctx, resolvedDate, settings.Timezone)
		if findErr == nil && existing.Status == logstore.DailyReportRunStatusPrepared {
			deliveries, deliveredRun, deliverErr := m.deliverPreparedDailyReport(ctx, existing.ID)
			if deliverErr != nil {
				return nil, deliverErr
			}
			return &DailyReportGenerateResult{Run: *deliveredRun, Deliveries: deliveries, Created: false}, nil
		}
		if findErr == nil && (existing.InternalContent != "" || existing.ExternalContent != "") {
			deliveries, listErr := m.dailyReports.ListDailyReportDeliveries(ctx, existing.ID)
			if listErr != nil {
				return nil, listErr
			}
			return &DailyReportGenerateResult{Run: *existing, Deliveries: deliveries, Created: false}, nil
		}
		if findErr != nil && !errors.Is(findErr, logstore.ErrNotFound) {
			return nil, findErr
		}
	}
	result, err := m.generateDailyReport(ctx, settings, resolvedDate, "manual", true)
	if err != nil || result.Run.Status != logstore.DailyReportRunStatusPrepared {
		return result, err
	}
	deliveries, deliveredRun, err := m.deliverPreparedDailyReport(ctx, result.Run.ID)
	if err != nil {
		return nil, err
	}
	result.Deliveries = deliveries
	result.Run = *deliveredRun
	return result, nil
}

func (m *Manager) DeliverDailyReportRun(ctx context.Context, runID string, audiences []logstore.DailyReportAudience) (*DailyReportRunDetail, error) {
	if m.dailyReports == nil {
		return nil, fmt.Errorf("daily reports require the logs store")
	}
	run, err := m.dailyReports.FindDailyReportRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.Status == logstore.DailyReportRunStatusRunning || run.Status == logstore.DailyReportRunStatusPrepared || run.CompletedAt == nil {
		return nil, fmt.Errorf("daily report is not in a completed state; generate or send the prepared report first")
	}
	selectedAudiences := normalizeDailyReportAudiences(audiences)
	for _, audience := range selectedAudiences {
		switch audience {
		case logstore.DailyReportAudienceInternal:
			if strings.TrimSpace(run.InternalContent) == "" {
				return nil, fmt.Errorf("daily report has no frozen internal content to resend")
			}
			_, _, _, err := m.deliverDailyReportAudience(ctx, run, audience, run.InternalContent, run.InternalChannelIDs, "manual resend")
			if err != nil {
				return nil, err
			}
		case logstore.DailyReportAudienceExternal:
			if strings.TrimSpace(run.ExternalContent) == "" {
				return nil, fmt.Errorf("daily report has no frozen external content to resend")
			}
			_, _, _, err := m.deliverDailyReportAudience(ctx, run, audience, run.ExternalContent, run.ExternalChannelIDs, "manual resend")
			if err != nil {
				return nil, err
			}
		}
	}
	// The original run is an immutable audit record once generated. A resend
	// only appends delivery attempts; current delivery state is derived on read.
	return m.GetDailyReportRunDetail(ctx, runID)
}

func (m *Manager) GetDailyReportRunDetail(ctx context.Context, runID string) (*DailyReportRunDetail, error) {
	if m.dailyReports == nil {
		return nil, fmt.Errorf("daily reports require the logs store")
	}
	run, err := m.dailyReports.FindDailyReportRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	deliveries, err := m.dailyReports.ListDailyReportDeliveries(ctx, runID)
	if err != nil {
		return nil, err
	}
	detail := &DailyReportRunDetail{Run: *run, Deliveries: deliveries}
	deriveDailyReportCurrentStatus(detail)
	return detail, nil
}

func (m *Manager) ListDailyReportRuns(ctx context.Context, query logstore.DailyReportHistoryQuery) ([]DailyReportRunDetail, int64, error) {
	if m.dailyReports == nil {
		return nil, 0, fmt.Errorf("daily reports require the logs store")
	}
	runs, total, err := m.dailyReports.ListDailyReportRuns(ctx, query)
	if err != nil {
		return nil, 0, err
	}
	details := make([]DailyReportRunDetail, 0, len(runs))
	for _, run := range runs {
		deliveries, deliveryErr := m.dailyReports.ListDailyReportDeliveries(ctx, run.ID)
		if deliveryErr != nil {
			return nil, 0, deliveryErr
		}
		detail := DailyReportRunDetail{Run: run, Deliveries: deliveries}
		deriveDailyReportCurrentStatus(&detail)
		details = append(details, detail)
	}
	return details, total, nil
}

func (m *Manager) maybeDispatchScheduledDailyReport(ctx context.Context) error {
	leader, err := m.ensureLeadership(ctx)
	if err != nil {
		return fmt.Errorf("daily report leadership check failed: %w", err)
	}
	if !leader {
		return nil
	}
	reportCtx, stopHeartbeat := m.withLeadershipHeartbeat(ctx)
	defer stopHeartbeat()
	ctx = reportCtx

	settings, err := m.GetDailyReportSettings(ctx)
	if err != nil {
		return err
	}
	if settings == nil || !settings.Enabled {
		return nil
	}
	generateHour, generateMinute, err := settings.GenerateHourMinute()
	if err != nil {
		return err
	}
	sendHour, sendMinute, err := settings.SendHourMinute()
	if err != nil {
		return err
	}
	location, err := time.LoadLocation(settings.Timezone)
	if err != nil {
		return err
	}
	nowLocal := m.currentTime().In(location)
	generateAt := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), generateHour, generateMinute, 0, 0, location)
	if nowLocal.Before(generateAt) {
		return nil
	}
	businessDate := nowLocal.AddDate(0, 0, -1).Format("2006-01-02")
	var run logstore.DailyReportRun
	existing, findErr := m.dailyReports.FindDailyReportRunByBusinessDate(ctx, businessDate, settings.Timezone)
	switch {
	case findErr == nil:
		run = *existing
		// A failed run is left for explicit operator retry; repeatedly launching
		// the same heavy query every sweep would amplify database pressure.
		if run.Status == logstore.DailyReportRunStatusFailed {
			return nil
		}
		if run.Status == logstore.DailyReportRunStatusRunning && m.currentTime().Sub(run.StartedAt) >= 15*time.Minute && nowLocal.Before(generateAt.Add(dailyReportGenerationGracePeriod)) {
			result, generateErr := m.generateDailyReport(ctx, settings, businessDate, "scheduled", false)
			if generateErr != nil && !errors.Is(generateErr, ErrDailyReportGenerationInProgress) {
				return generateErr
			}
			if generateErr == nil {
				run = result.Run
			}
		}
	case errors.Is(findErr, logstore.ErrNotFound):
		// Do not run a missed heavy query later in the business day. The grace
		// window lets brief restarts recover while keeping auto-generation inside
		// the configured off-peak period.
		if nowLocal.After(generateAt.Add(dailyReportGenerationGracePeriod)) {
			return nil
		}
		result, generateErr := m.generateDailyReport(ctx, settings, businessDate, "scheduled", false)
		if errors.Is(generateErr, ErrDailyReportGenerationInProgress) {
			return nil
		}
		if generateErr != nil {
			return generateErr
		}
		run = result.Run
	default:
		return findErr
	}
	sendAt := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), sendHour, sendMinute, 0, 0, location)
	if nowLocal.Before(sendAt) || run.Status != logstore.DailyReportRunStatusPrepared {
		return nil
	}
	_, _, err = m.deliverPreparedDailyReport(ctx, run.ID)
	return err
}

func (m *Manager) syncDailyReportConfig(ctx context.Context, spec *DailyReportSpec) error {
	settings := defaultDailyReportSettings()
	if spec.Enabled != nil {
		settings.Enabled = *spec.Enabled
	}
	if spec.Timezone != "" {
		settings.Timezone = spec.Timezone
	}
	if spec.GenerateTime != "" {
		settings.GenerateTime = spec.GenerateTime
	}
	if spec.SendTime != "" {
		settings.SendTime = spec.SendTime
	}
	if spec.SlowThresholdMs != nil {
		settings.SlowThresholdMs = *spec.SlowThresholdMs
	}
	if spec.InternalEnabled != nil {
		settings.InternalEnabled = *spec.InternalEnabled
	}
	settings.InternalChannelIDs = append([]string(nil), spec.InternalChannelIDs...)
	if spec.ExternalEnabled != nil {
		settings.ExternalEnabled = *spec.ExternalEnabled
	}
	settings.ExternalChannelIDs = append([]string(nil), spec.ExternalChannelIDs...)
	_, err := m.UpdateDailyReportSettings(ctx, settings)
	return err
}

func (m *Manager) generateDailyReport(
	ctx context.Context,
	settings *tables.TableDailyReportSettings,
	businessDate string,
	trigger string,
	deliver bool,
) (*DailyReportGenerateResult, error) {
	return m.generateDailyReportWithProgress(ctx, settings, businessDate, trigger, deliver, nil)
}

func (m *Manager) generateDailyReportWithProgress(
	ctx context.Context,
	settings *tables.TableDailyReportSettings,
	businessDate string,
	trigger string,
	deliver bool,
	progress func(logstore.DailyReportMetricsProgress),
) (*DailyReportGenerateResult, error) {
	if m.dailyReports == nil {
		return nil, fmt.Errorf("daily reports require the logs store")
	}
	activeSettings := cloneDailyReportSettings(settings)
	applyDailyReportDefaults(activeSettings)
	if err := m.validateDailyReportSettings(ctx, activeSettings); err != nil {
		return nil, err
	}
	if deliver {
		if err := m.validateDailyReportDeliveryTargets(ctx, activeSettings); err != nil {
			return nil, err
		}
	}
	if businessDate == "" {
		location, err := time.LoadLocation(activeSettings.Timezone)
		if err != nil {
			return nil, err
		}
		businessDate = time.Now().In(location).AddDate(0, 0, -1).Format("2006-01-02")
	}
	runKey := activeSettings.Timezone + "\x00" + businessDate
	if _, loaded := m.reportRuns.LoadOrStore(runKey, struct{}{}); loaded {
		return nil, ErrDailyReportGenerationInProgress
	}
	defer m.reportRuns.Delete(runKey)
	releaseDistributedLock, err := m.acquireDailyReportGenerationLock(ctx, activeSettings.Timezone, businessDate)
	if err != nil {
		return nil, err
	}
	if releaseDistributedLock == nil {
		return nil, ErrDailyReportGenerationInProgress
	}
	defer releaseDistributedLock()
	var run *logstore.DailyReportRun
	existing, findErr := m.dailyReports.FindDailyReportRunByBusinessDate(ctx, businessDate, activeSettings.Timezone)
	if findErr == nil {
		// Completed snapshots are immutable: generation is idempotent and any
		// delivery retry reuses their captured content/channel IDs.
		if existing.InternalContent != "" || existing.ExternalContent != "" {
			deliveries, deliveryErr := m.dailyReports.ListDailyReportDeliveries(ctx, existing.ID)
			if deliveryErr != nil {
				return nil, deliveryErr
			}
			return &DailyReportGenerateResult{Run: *existing, Deliveries: deliveries, Created: false}, nil
		}
		// A fresh placeholder belongs to another in-flight generator. An old or
		// failed placeholder is safe to resume after a process crash/query error.
		if existing.Status == logstore.DailyReportRunStatusRunning && m.currentTime().Sub(existing.StartedAt) < 15*time.Minute {
			return nil, ErrDailyReportGenerationInProgress
		}
		run = existing
		run.Trigger = trigger
		run.Status = logstore.DailyReportRunStatusRunning
		run.InternalStatus = initialDailyAudienceStatus(activeSettings.InternalEnabled, activeSettings.InternalChannelIDs)
		run.ExternalStatus = initialDailyAudienceStatus(activeSettings.ExternalEnabled, activeSettings.ExternalChannelIDs)
		run.InternalStatusDetail = ""
		run.ExternalStatusDetail = ""
		run.InternalChannelIDs = append([]string(nil), activeSettings.InternalChannelIDs...)
		run.ExternalChannelIDs = append([]string(nil), activeSettings.ExternalChannelIDs...)
		run.StartedAt = time.Now().UTC()
		run.CompletedAt = nil
	} else if !errors.Is(findErr, logstore.ErrNotFound) {
		return nil, findErr
	}
	created := run == nil
	if created {
		run = &logstore.DailyReportRun{
			ID:                 dailyReportRunID(activeSettings.Timezone, businessDate),
			BusinessDate:       businessDate,
			Timezone:           activeSettings.Timezone,
			SlowThresholdMs:    activeSettings.SlowThresholdMs,
			Trigger:            trigger,
			Status:             logstore.DailyReportRunStatusRunning,
			InternalStatus:     initialDailyAudienceStatus(activeSettings.InternalEnabled, activeSettings.InternalChannelIDs),
			ExternalStatus:     initialDailyAudienceStatus(activeSettings.ExternalEnabled, activeSettings.ExternalChannelIDs),
			GeneratedAt:        time.Now().UTC(),
			InternalChannelIDs: append([]string(nil), activeSettings.InternalChannelIDs...),
			ExternalChannelIDs: append([]string(nil), activeSettings.ExternalChannelIDs...),
			StartedAt:          time.Now().UTC(),
			CreatedAt:          time.Now().UTC(),
		}
	}
	windowStart, windowEnd, err := dailyReportWindow(activeSettings.Timezone, businessDate)
	if err != nil {
		return nil, err
	}
	run.WindowStart = windowStart
	run.WindowEnd = windowEnd
	if created {
		if err := m.dailyReports.CreateDailyReportRun(ctx, run); err != nil {
			if isLikelyUniqueConstraint(err) {
				existing, lookupErr := m.dailyReports.FindDailyReportRunByBusinessDate(ctx, businessDate, activeSettings.Timezone)
				if lookupErr != nil {
					return nil, err
				}
				deliveries, deliveryErr := m.dailyReports.ListDailyReportDeliveries(ctx, existing.ID)
				if deliveryErr != nil {
					return nil, deliveryErr
				}
				return &DailyReportGenerateResult{Run: *existing, Deliveries: deliveries, Created: false}, nil
			}
			return nil, err
		}
	} else if err := m.updateDailyReportRun(ctx, run); err != nil {
		return nil, err
	}
	snapshot, err := m.buildDailyReportSnapshotWithProgress(ctx, activeSettings, businessDate, progress)
	if err != nil {
		run.Status = logstore.DailyReportRunStatusFailed
		run.InternalStatus = collapseDailyPendingStatus(run.InternalStatus)
		run.ExternalStatus = collapseDailyPendingStatus(run.ExternalStatus)
		run.InternalStatusDetail = err.Error()
		run.ExternalStatusDetail = err.Error()
		completed := time.Now().UTC()
		run.CompletedAt = &completed
		_ = m.updateDailyReportRun(ctx, run)
		return nil, err
	}
	run.BusinessDate = snapshot.BusinessDate
	run.Timezone = snapshot.Timezone
	run.WindowStart = snapshot.WindowStart
	run.WindowEnd = snapshot.WindowEnd
	run.SlowThresholdMs = snapshot.SlowThresholdMs
	run.GeneratedAt = snapshot.GeneratedAt
	run.Snapshot = *snapshot
	run.InternalContent = renderInternalDailyReport(*snapshot)
	run.ExternalContent = renderExternalDailyReport(snapshot.PublicView())

	if !deliver {
		run.Status = logstore.DailyReportRunStatusPrepared
		run.CompletedAt = nil
		if err := m.updateDailyReportRun(ctx, run); err != nil {
			return nil, err
		}
		return &DailyReportGenerateResult{Run: *run, Created: created}, nil
	}
	deliveries, err := m.deliverInitialDailyReport(ctx, run)
	if err != nil {
		return nil, err
	}
	return &DailyReportGenerateResult{Run: *run, Deliveries: deliveries, Created: created}, nil
}

func (m *Manager) deliverInitialDailyReport(ctx context.Context, run *logstore.DailyReportRun) ([]logstore.DailyReportDelivery, error) {
	deliveries := make([]logstore.DailyReportDelivery, 0)
	if run.InternalStatus != logstore.DailyReportAudienceStatusNotEnabled && len(run.InternalChannelIDs) > 0 {
		audienceDeliveries, audienceStatus, audienceDetail, err := m.deliverDailyReportAudience(ctx, run, logstore.DailyReportAudienceInternal, run.InternalContent, run.InternalChannelIDs, "")
		if err != nil {
			m.finalizeDailyReportDeliveryFailure(ctx, run, logstore.DailyReportAudienceInternal, err)
			return nil, err
		}
		run.InternalStatus = audienceStatus
		run.InternalStatusDetail = audienceDetail
		deliveries = append(deliveries, audienceDeliveries...)
	}
	if run.ExternalStatus != logstore.DailyReportAudienceStatusNotEnabled && len(run.ExternalChannelIDs) > 0 {
		audienceDeliveries, audienceStatus, audienceDetail, err := m.deliverDailyReportAudience(ctx, run, logstore.DailyReportAudienceExternal, run.ExternalContent, run.ExternalChannelIDs, "")
		if err != nil {
			m.finalizeDailyReportDeliveryFailure(ctx, run, logstore.DailyReportAudienceExternal, err)
			return nil, err
		}
		run.ExternalStatus = audienceStatus
		run.ExternalStatusDetail = audienceDetail
		deliveries = append(deliveries, audienceDeliveries...)
	}
	run.Status = overallDailyRunStatus(run.InternalStatus, run.ExternalStatus)
	completed := time.Now().UTC()
	run.CompletedAt = &completed
	if err := m.updateDailyReportRun(ctx, run); err != nil {
		return nil, err
	}
	return deliveries, nil
}

func (m *Manager) deliverPreparedDailyReport(ctx context.Context, runID string) ([]logstore.DailyReportDelivery, *logstore.DailyReportRun, error) {
	run, err := m.dailyReports.FindDailyReportRun(ctx, runID)
	if err != nil {
		return nil, nil, err
	}
	release, err := m.acquireDailyReportGenerationLock(ctx, run.Timezone, run.BusinessDate)
	if err != nil {
		return nil, nil, err
	}
	if release == nil {
		return nil, nil, ErrDailyReportGenerationInProgress
	}
	defer release()
	run, err = m.dailyReports.FindDailyReportRun(ctx, runID)
	if err != nil {
		return nil, nil, err
	}
	if run.Status != logstore.DailyReportRunStatusPrepared {
		deliveries, listErr := m.dailyReports.ListDailyReportDeliveries(ctx, runID)
		return deliveries, run, listErr
	}
	deliveries, err := m.deliverInitialDailyReport(ctx, run)
	return deliveries, run, err
}

func (m *Manager) finalizeDailyReportDeliveryFailure(
	ctx context.Context,
	run *logstore.DailyReportRun,
	audience logstore.DailyReportAudience,
	deliveryErr error,
) {
	if run == nil {
		return
	}
	detail := "delivery persistence failed: " + deliveryErr.Error()
	if audience == logstore.DailyReportAudienceInternal {
		run.InternalStatus = logstore.DailyReportAudienceStatusFailed
		run.InternalStatusDetail = detail
	} else {
		run.ExternalStatus = logstore.DailyReportAudienceStatusFailed
		run.ExternalStatusDetail = detail
	}
	if run.InternalStatus == logstore.DailyReportAudienceStatusPending {
		run.InternalStatus = logstore.DailyReportAudienceStatusFailed
		run.InternalStatusDetail = "delivery skipped after an earlier persistence failure"
	}
	if run.ExternalStatus == logstore.DailyReportAudienceStatusPending {
		run.ExternalStatus = logstore.DailyReportAudienceStatusFailed
		run.ExternalStatusDetail = "delivery skipped after an earlier persistence failure"
	}
	run.Status = overallDailyRunStatus(run.InternalStatus, run.ExternalStatus)
	completed := time.Now().UTC()
	run.CompletedAt = &completed
	if err := m.updateDailyReportRun(ctx, run); err != nil && m.logger != nil {
		m.logger.Error("daily report: failed to persist terminal delivery error: %v", err)
	}
}

func (m *Manager) buildDailyReportSnapshot(ctx context.Context, settings *tables.TableDailyReportSettings, businessDate string) (*logstore.DailyReportSnapshot, error) {
	return m.buildDailyReportSnapshotWithProgress(ctx, settings, businessDate, nil)
}

func (m *Manager) buildDailyReportSnapshotWithProgress(
	ctx context.Context,
	settings *tables.TableDailyReportSettings,
	businessDate string,
	progress func(logstore.DailyReportMetricsProgress),
) (*logstore.DailyReportSnapshot, error) {
	if m.dailyReports == nil {
		return nil, fmt.Errorf("daily reports require the logs store")
	}
	if !m.reportQueryMu.TryLock() {
		return nil, ErrDailyReportQueryInProgress
	}
	defer m.reportQueryMu.Unlock()
	releaseQueryLock, err := m.acquireDailyReportLock(ctx, "bifrost:daily-report:query", dailyReportMaxQueryDuration+5*time.Minute)
	if err != nil {
		return nil, err
	}
	if releaseQueryLock == nil {
		return nil, ErrDailyReportQueryInProgress
	}
	defer releaseQueryLock()
	if businessDate == "" {
		location, err := time.LoadLocation(settings.Timezone)
		if err != nil {
			return nil, err
		}
		businessDate = time.Now().In(location).AddDate(0, 0, -1).Format("2006-01-02")
	}
	windowStart, windowEnd, err := dailyReportWindow(settings.Timezone, businessDate)
	if err != nil {
		return nil, err
	}
	queryCtx, cancel := context.WithTimeout(ctx, dailyReportMaxQueryDuration)
	defer cancel()
	return m.dailyReports.BuildDailyReportSnapshot(queryCtx, logstore.DailyReportMetricsQuery{
		BusinessDate:    businessDate,
		Timezone:        settings.Timezone,
		WindowStart:     windowStart,
		WindowEnd:       windowEnd,
		SlowThresholdMs: settings.SlowThresholdMs,
		GeneratedAt:     time.Now().UTC(),
		Progress:        progress,
	})
}

func (m *Manager) validateDailyReportSettings(ctx context.Context, settings *tables.TableDailyReportSettings) error {
	if settings == nil {
		return fmt.Errorf("daily report settings are required")
	}
	applyDailyReportDefaults(settings)
	if err := settings.Validate(); err != nil {
		return err
	}
	settings.InternalChannelIDs = normalizeStringIDs(settings.InternalChannelIDs)
	settings.ExternalChannelIDs = normalizeStringIDs(settings.ExternalChannelIDs)
	if !settings.Enabled {
		return nil
	}
	return m.validateDailyReportDeliveryTargets(ctx, settings)
}

func (m *Manager) validateDailyReportDeliveryTargets(ctx context.Context, settings *tables.TableDailyReportSettings) error {
	if !settings.InternalEnabled && !settings.ExternalEnabled {
		return fmt.Errorf("enable at least one audience or disable the daily report")
	}
	if settings.InternalEnabled && len(settings.InternalChannelIDs) == 0 {
		return fmt.Errorf("select at least one internal channel")
	}
	if settings.ExternalEnabled && len(settings.ExternalChannelIDs) == 0 {
		return fmt.Errorf("select at least one external channel")
	}
	channelIDs := append(append([]string(nil), settings.InternalChannelIDs...), settings.ExternalChannelIDs...)
	for _, channelID := range channelIDs {
		channel, err := m.store.GetAlertChannel(ctx, channelID)
		if err != nil {
			return fmt.Errorf("alert channel %q does not exist", channelID)
		}
		if !supportsDailyReportChannel(channel.Type) {
			return fmt.Errorf("alert channel %q uses unsupported type %q for daily reports", channelID, channel.Type)
		}
	}
	return nil
}

func (m *Manager) deliverDailyReportAudience(
	ctx context.Context,
	run *logstore.DailyReportRun,
	audience logstore.DailyReportAudience,
	content string,
	channelIDs []string,
	detail string,
) ([]logstore.DailyReportDelivery, logstore.DailyReportAudienceStatus, string, error) {
	existingDeliveries, err := m.dailyReports.ListDailyReportDeliveries(ctx, run.ID)
	if err != nil {
		return nil, logstore.DailyReportAudienceStatusFailed, "", err
	}
	deliveries := make([]logstore.DailyReportDelivery, 0, len(channelIDs))
	successCount := 0
	failureCount := 0
	failureDetails := make([]string, 0)
	for _, channelID := range normalizeStringIDs(channelIDs) {
		delivery := logstore.DailyReportDelivery{
			ID:          uuid.NewString(),
			RunID:       run.ID,
			Audience:    audience,
			ChannelID:   channelID,
			AttemptNo:   nextDailyReportAttempt(existingDeliveries, audience, channelID),
			CreatedAt:   time.Now().UTC(),
			Status:      logstore.DailyReportDeliveryStatusDelivered,
			ChannelType: "",
		}
		channel, getErr := m.store.GetAlertChannel(ctx, channelID)
		if getErr != nil {
			delivery.Status = logstore.DailyReportDeliveryStatusFailed
			delivery.StatusDetail = "alert channel not found"
			if err := m.dailyReports.CreateDailyReportDelivery(ctx, &delivery); err != nil {
				return nil, logstore.DailyReportAudienceStatusFailed, "", err
			}
			failureCount++
			failureDetails = append(failureDetails, fmt.Sprintf("%s: %s", channelID, delivery.StatusDetail))
			deliveries = append(deliveries, delivery)
			continue
		}
		delivery.ChannelName = channel.Name
		delivery.ChannelType = channel.Type
		if !channel.Enabled {
			delivery.Status = logstore.DailyReportDeliveryStatusFailed
			delivery.StatusDetail = "alert channel is disabled"
		} else if err := m.sendDailyReport(ctx, channel, run, audience, content); err != nil {
			delivery.Status = logstore.DailyReportDeliveryStatusFailed
			delivery.StatusDetail = err.Error()
		} else {
			delivery.StatusDetail = detail
		}
		if err := m.dailyReports.CreateDailyReportDelivery(ctx, &delivery); err != nil {
			return nil, logstore.DailyReportAudienceStatusFailed, "", err
		}
		if delivery.Status == logstore.DailyReportDeliveryStatusDelivered {
			successCount++
		} else {
			failureCount++
			failureDetails = append(failureDetails, fmt.Sprintf("%s: %s", channel.Name, delivery.StatusDetail))
		}
		deliveries = append(deliveries, delivery)
	}
	switch {
	case successCount > 0 && failureCount == 0:
		return deliveries, logstore.DailyReportAudienceStatusDelivered, fmt.Sprintf("已发送 %d 个渠道", successCount), nil
	case successCount > 0:
		return deliveries, logstore.DailyReportAudienceStatusFailed, fmt.Sprintf("已发送 %d 个渠道，失败 %d 个：%s", successCount, failureCount, strings.Join(failureDetails, "；")), nil
	case len(channelIDs) == 0:
		return deliveries, logstore.DailyReportAudienceStatusNoChannels, "未配置渠道", nil
	default:
		return deliveries, logstore.DailyReportAudienceStatusFailed, strings.Join(failureDetails, "；"), nil
	}
}

func (m *Manager) sendDailyReport(
	ctx context.Context,
	channel *tables.TableAlertChannel,
	run *logstore.DailyReportRun,
	audience logstore.DailyReportAudience,
	content string,
) error {
	title := dailyReportTitle(run.BusinessDate, audience)
	now := time.Now().UTC()
	metadata := dailyReportRunMetadata(run)
	snapshot := dailyReportSnapshotPayload(run, audience)
	return m.SendNotification(ctx, channel, ChannelNotification{
		Title:     title,
		Text:      content,
		Markdown:  weComDailyReportMarkdown(run, audience),
		Event:     fmt.Sprintf("daily-report:%s:%s", run.BusinessDate, audience),
		Details:   map[string]any{"audience": audience, "report": metadata, "snapshot": snapshot},
		Payload:   map[string]any{"event": "daily_report." + string(audience), "timestamp": now, "audience": audience, "title": title, "content": content, "report": metadata, "snapshot": snapshot},
		Severity:  "info",
		Source:    "Bifrost Daily Reports",
		Timestamp: now,
	})
}

// dailyReportRunMetadata deliberately excludes rendered content and the
// internal snapshot. The external webhook payload is built from this allowlist
// so provider/model details cannot leak through a nested run object.
func dailyReportRunMetadata(run *logstore.DailyReportRun) map[string]any {
	return map[string]any{
		"id":                run.ID,
		"business_date":     run.BusinessDate,
		"timezone":          run.Timezone,
		"window_start":      run.WindowStart,
		"window_end":        run.WindowEnd,
		"slow_threshold_ms": run.SlowThresholdMs,
		"generated_at":      run.GeneratedAt,
	}
}

func (m *Manager) updateDailyReportRun(ctx context.Context, run *logstore.DailyReportRun) error {
	updates, err := run.UpdateMap()
	if err != nil {
		return err
	}
	return m.dailyReports.UpdateDailyReportRun(ctx, run.ID, updates)
}

func dailyReportSnapshotPayload(run *logstore.DailyReportRun, audience logstore.DailyReportAudience) any {
	if audience == logstore.DailyReportAudienceExternal {
		return run.Snapshot.PublicView()
	}
	return run.Snapshot
}

func weComDailyReportMarkdown(run *logstore.DailyReportRun, audience logstore.DailyReportAudience) string {
	lines := []string{
		fmt.Sprintf("## %s", dailyReportTitle(run.BusinessDate, audience)),
		fmt.Sprintf("> 时区：%s", markdownInline(run.Timezone)),
		fmt.Sprintf("> 统计日期：%s", markdownInline(run.BusinessDate)),
		fmt.Sprintf("> 请求数：%d", run.Snapshot.Overview.UserRequests),
		fmt.Sprintf("> 用户成功率：<font color=\"info\">%.2f%%</font>", run.Snapshot.Overview.UserSuccessRate),
		fmt.Sprintf("> 自动恢复：%d（%.2f%%）", run.Snapshot.Overview.FallbackRecoveries, run.Snapshot.Overview.FallbackRecoveryRate),
		fmt.Sprintf("> 慢请求：%d（%.2f%%）", run.Snapshot.Overview.SlowRequests, run.Snapshot.Overview.SlowRequestRate),
		fmt.Sprintf("> 平均 / P95：%s / %s", formatDailyLatency(run.Snapshot.Overview.AverageLatencyMs), formatDailyLatency(run.Snapshot.Overview.P95LatencyMs)),
	}
	if audience == logstore.DailyReportAudienceInternal {
		for _, provider := range takeDailyProviders(run.Snapshot.Providers, 6) {
			lines = append(lines, fmt.Sprintf("> %s：%d 次，成功率 %.2f%%，P95 %s", markdownInline(provider.Provider), provider.Attempts, provider.SuccessRate, formatDailyLatency(provider.P95LatencyMs)))
		}
		if len(run.Snapshot.Providers) > 6 {
			lines = append(lines, fmt.Sprintf("> 其余 %d 个供应商已省略，请在 Bifrost Dashboard 查看完整内部报告。", len(run.Snapshot.Providers)-6))
		}
	}
	return truncateUTF8Bytes(strings.Join(lines, "\n"), maxWeComMarkdownBytes)
}

func renderInternalDailyReport(snapshot logstore.DailyReportSnapshot) string {
	windowStart, windowEnd := formatDailyReportWindow(snapshot)
	lines := []string{
		fmt.Sprintf("Bifrost 每日供应商质量日报 | %s | %s", snapshot.BusinessDate, snapshot.Timezone),
		fmt.Sprintf("统计窗口: %s 至 %s", windowStart, windowEnd),
		"",
		"总览",
		fmt.Sprintf("- 用户请求数: %d", snapshot.Overview.UserRequests),
		fmt.Sprintf("- 供应商尝试数: %d", snapshot.Overview.ProviderAttempts),
		fmt.Sprintf("- 用户成功率: %.2f%%", snapshot.Overview.UserSuccessRate),
		fmt.Sprintf("- 系统成功率: %.2f%%", snapshot.Overview.SystemSuccessRate),
		fmt.Sprintf("- 自动恢复: %d (%.2f%%)", snapshot.Overview.FallbackRecoveries, snapshot.Overview.FallbackRecoveryRate),
		fmt.Sprintf("- 重试次数: %d", snapshot.Overview.RetryCount),
		fmt.Sprintf("- 慢请求: %d (%.2f%%, 阈值 %s)", snapshot.Overview.SlowRequests, snapshot.Overview.SlowRequestRate, formatDailyLatency(float64(snapshot.SlowThresholdMs))),
		fmt.Sprintf("- 平均 / P95 / P99 延迟: %s / %s / %s", formatDailyLatency(snapshot.Overview.AverageLatencyMs), formatDailyLatency(snapshot.Overview.P95LatencyMs), formatDailyLatency(snapshot.Overview.P99LatencyMs)),
		"",
		"错误分布",
	}
	if len(snapshot.Overview.ErrorBuckets) == 0 {
		lines = append(lines, "- 无")
	} else {
		for _, bucket := range snapshot.Overview.ErrorBuckets {
			lines = append(lines, fmt.Sprintf("- %s: %d (%.2f%%)", bucket.Label, bucket.Count, bucket.Rate))
		}
	}
	lines = append(lines, "", "趋势对比")
	lines = append(lines, renderTrendLine("用户请求数", snapshot.Trends.UserRequests))
	lines = append(lines, renderTrendLine("用户成功率", snapshot.Trends.UserSuccessRate))
	lines = append(lines, renderTrendLine("系统成功率", snapshot.Trends.SystemSuccessRate))
	lines = append(lines, renderTrendLine("自动恢复次数", snapshot.Trends.FallbackRecoveries))
	lines = append(lines, renderTrendLine("慢请求率", snapshot.Trends.SlowRequestRate))
	lines = append(lines, renderTrendLine("平均延迟", snapshot.Trends.AverageLatencyMs))
	lines = append(lines, "", "供应商明细")
	if len(snapshot.Providers) == 0 {
		lines = append(lines, "无请求数据")
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "供应商 | 请求数 | 成功率 | 慢请求 | 平均延迟 | P95 | 重试 | 主要错误")
	lines = append(lines, "--- | ---: | ---: | ---: | ---: | ---: | ---: | ---")
	for _, provider := range snapshot.Providers {
		lines = append(lines, fmt.Sprintf(
			"%s | %d | %.2f%% | %d | %s | %s | %d | %s",
			provider.Provider,
			provider.Attempts,
			provider.SuccessRate,
			provider.SlowRequests,
			formatDailyLatency(provider.AverageLatencyMs),
			formatDailyLatency(provider.P95LatencyMs),
			provider.RetryCount,
			formatDailyErrors(provider.ErrorBuckets),
		))
	}
	lines = append(lines, "", "模型明细")
	lines = append(lines, "供应商 | 模型 | 请求数 | 成功率 | 慢请求 | 平均延迟 | P95 | 重试 | 主要错误")
	lines = append(lines, "--- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---")
	for _, provider := range snapshot.Providers {
		for _, model := range provider.Models {
			modelName := model.Model
			if strings.TrimSpace(modelName) == "" {
				modelName = "(未记录)"
			}
			lines = append(lines, fmt.Sprintf(
				"%s | %s | %d | %.2f%% | %d | %s | %s | %d | %s",
				provider.Provider,
				modelName,
				model.Attempts,
				model.SuccessRate,
				model.SlowRequests,
				formatDailyLatency(model.AverageLatencyMs),
				formatDailyLatency(model.P95LatencyMs),
				model.RetryCount,
				formatDailyErrors(model.ErrorBuckets),
			))
		}
	}
	return strings.Join(lines, "\n")
}

func renderExternalDailyReport(snapshot logstore.PublicDailyReportSnapshot) string {
	lines := []string{
		fmt.Sprintf("Bifrost 每日服务质量回顾 | %s | %s", snapshot.BusinessDate, snapshot.Timezone),
		"",
		"服务概况",
		fmt.Sprintf("- 请求数: %d", snapshot.UserRequests),
		fmt.Sprintf("- 用户成功率: %.2f%%", snapshot.UserSuccessRate),
		fmt.Sprintf("- 平均 / P95 / P99 延迟: %s / %s / %s", formatDailyLatency(snapshot.AverageLatencyMs), formatDailyLatency(snapshot.P95LatencyMs), formatDailyLatency(snapshot.P99LatencyMs)),
		fmt.Sprintf("- 慢请求: %d (%.2f%%, 阈值 %s)", snapshot.SlowRequests, snapshot.SlowRequestRate, formatDailyLatency(float64(snapshot.SlowThresholdMs))),
		fmt.Sprintf("- 平台自动恢复: %d (%.2f%%)", snapshot.FallbackRecoveries, snapshot.FallbackRecoveryRate),
		"",
		"错误类型分布",
	}
	if len(snapshot.ErrorBuckets) == 0 {
		lines = append(lines, "- 无")
	} else {
		for _, bucket := range snapshot.ErrorBuckets {
			lines = append(lines, fmt.Sprintf("- %s: %d (%.2f%%)", bucket.Label, bucket.Count, bucket.Rate))
		}
	}
	lines = append(lines, "", "与前一日相比")
	lines = append(lines, renderTrendLine("请求数", snapshot.Trends.UserRequests))
	lines = append(lines, renderTrendLine("成功率", snapshot.Trends.UserSuccessRate))
	lines = append(lines, renderTrendLine("自动恢复次数", snapshot.Trends.FallbackRecoveries))
	lines = append(lines, renderTrendLine("慢请求率", snapshot.Trends.SlowRequestRate))
	lines = append(lines, renderTrendLine("平均延迟", snapshot.Trends.AverageLatencyMs))
	lines = append(lines, "", "我们会持续根据这些数据优化供应商稳定性、恢复策略与慢请求治理。")
	return strings.Join(lines, "\n")
}

func renderTrendLine(label string, value logstore.DailyReportTrendValue) string {
	sign := "+"
	if value.Delta < 0 {
		sign = ""
	}
	return fmt.Sprintf("- %s: 当前 %.2f，前一日 %.2f，变化 %s%.2f (%s%.2f%%)", label, value.Current, value.Previous, sign, value.Delta, sign, value.DeltaPercentage)
}

func formatDailyErrors(buckets []logstore.DailyReportErrorBucket) string {
	if len(buckets) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(buckets))
	for _, bucket := range buckets {
		parts = append(parts, fmt.Sprintf("%s %d", bucket.Label, bucket.Count))
	}
	return strings.Join(parts, " / ")
}

func normalizeDailyReportAudiences(audiences []logstore.DailyReportAudience) []logstore.DailyReportAudience {
	if len(audiences) == 0 {
		return []logstore.DailyReportAudience{logstore.DailyReportAudienceInternal, logstore.DailyReportAudienceExternal}
	}
	seen := make(map[logstore.DailyReportAudience]struct{}, len(audiences))
	result := make([]logstore.DailyReportAudience, 0, len(audiences))
	for _, audience := range audiences {
		if audience == "" {
			continue
		}
		if _, ok := seen[audience]; ok {
			continue
		}
		seen[audience] = struct{}{}
		result = append(result, audience)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func normalizeStringIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func supportsDailyReportChannel(channelType string) bool {
	switch channelType {
	case tables.AlertChannelSlack, tables.AlertChannelMicrosoftTeams, tables.AlertChannelWeCom, tables.AlertChannelWebhook:
		return true
	default:
		return false
	}
}

func dailyReportTitle(businessDate string, audience logstore.DailyReportAudience) string {
	if audience == logstore.DailyReportAudienceExternal {
		return fmt.Sprintf("Bifrost 每日服务质量回顾 | %s", businessDate)
	}
	return fmt.Sprintf("Bifrost 每日供应商质量日报 | %s", businessDate)
}

func defaultDailyReportSettings() *tables.TableDailyReportSettings {
	return &tables.TableDailyReportSettings{
		ID:              tables.DefaultDailyReportSettingsID,
		Enabled:         false,
		Timezone:        defaultDailyReportTimezone,
		GenerateTime:    defaultDailyReportGenerateTime,
		SendTime:        defaultDailyReportSendTime,
		SlowThresholdMs: defaultDailyReportSlowThresholdMs,
		InternalEnabled: true,
	}
}

func cloneDailyReportSettings(settings *tables.TableDailyReportSettings) *tables.TableDailyReportSettings {
	if settings == nil {
		return defaultDailyReportSettings()
	}
	cloned := *settings
	cloned.InternalChannelIDs = append([]string(nil), settings.InternalChannelIDs...)
	cloned.ExternalChannelIDs = append([]string(nil), settings.ExternalChannelIDs...)
	return &cloned
}

func applyDailyReportDefaults(settings *tables.TableDailyReportSettings) {
	if settings == nil {
		return
	}
	if settings.ID == "" {
		settings.ID = tables.DefaultDailyReportSettingsID
	}
	if settings.Timezone == "" {
		settings.Timezone = defaultDailyReportTimezone
	}
	if settings.GenerateTime == "" {
		settings.GenerateTime = defaultDailyReportGenerateTime
	}
	if settings.SendTime == "" {
		settings.SendTime = defaultDailyReportSendTime
	}
	settings.InternalChannelIDs = normalizeStringIDs(settings.InternalChannelIDs)
	settings.ExternalChannelIDs = normalizeStringIDs(settings.ExternalChannelIDs)
}

func (m *Manager) currentTime() time.Time {
	if m != nil && m.now != nil {
		return m.now()
	}
	return time.Now()
}

func resolveDailyReportBusinessDate(settings *tables.TableDailyReportSettings, businessDate string, now time.Time) (string, error) {
	if businessDate != "" {
		if _, _, err := dailyReportWindow(settings.Timezone, businessDate); err != nil {
			return "", err
		}
		return businessDate, nil
	}
	location, err := time.LoadLocation(settings.Timezone)
	if err != nil {
		return "", err
	}
	return now.In(location).AddDate(0, 0, -1).Format("2006-01-02"), nil
}

func dailyReportRunID(timezone, businessDate string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("bifrost:daily-report:"+timezone+":"+businessDate)).String()
}

func (m *Manager) acquireDailyReportGenerationLock(ctx context.Context, timezone, businessDate string) (func(), error) {
	return m.acquireDailyReportLock(ctx, "bifrost:daily-report:"+dailyReportRunID(timezone, businessDate), 30*time.Minute)
}

func (m *Manager) acquireDailyReportLock(ctx context.Context, lockKey string, ttl time.Duration) (func(), error) {
	if m.leaderStore == nil {
		return func() {}, nil
	}
	now := time.Now().UTC()
	lock, err := m.leaderStore.GetLock(ctx, lockKey)
	if err != nil {
		return nil, err
	}
	if lock != nil && lock.ExpiresAt.After(now) {
		return nil, nil
	}
	if lock != nil {
		if _, err := m.leaderStore.CleanupExpiredLockByKey(ctx, lockKey); err != nil {
			return nil, err
		}
	}
	holderID := m.holderID + ":" + uuid.NewString()
	acquired, err := m.leaderStore.TryAcquireLock(ctx, &tables.TableDistributedLock{
		LockKey:   lockKey,
		HolderID:  holderID,
		ExpiresAt: now.Add(ttl),
	})
	if err != nil || !acquired {
		return nil, err
	}
	return func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if store, ok := m.leaderStore.(dailyReportReleaseLockStore); ok {
			if _, err := store.ReleaseLock(releaseCtx, lockKey, holderID); err != nil && m.logger != nil {
				m.logger.Warn("daily report: failed to release generation lock: %v", err)
			}
			return
		}
		if err := m.leaderStore.UpdateLockExpiry(releaseCtx, lockKey, holderID, time.Now().UTC().Add(-time.Second)); err == nil {
			_, _ = m.leaderStore.CleanupExpiredLockByKey(releaseCtx, lockKey)
		}
	}, nil
}

func dailyReportWindow(timezone, businessDate string) (time.Time, time.Time, error) {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	day, err := time.ParseInLocation("2006-01-02", businessDate, location)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("business_date must use YYYY-MM-DD")
	}
	return day.UTC(), day.AddDate(0, 0, 1).UTC(), nil
}

func nextDailyReportAttempt(deliveries []logstore.DailyReportDelivery, audience logstore.DailyReportAudience, channelID string) int {
	attempt := 1
	for _, delivery := range deliveries {
		if delivery.ChannelID == channelID && delivery.Audience == audience && delivery.AttemptNo >= attempt {
			attempt = delivery.AttemptNo + 1
		}
	}
	return attempt
}

func formatDailyLatency(ms float64) string {
	if ms >= 1000 {
		return fmt.Sprintf("%.2fs", ms/1000)
	}
	return fmt.Sprintf("%.0fms", ms)
}

func formatDailyReportWindow(snapshot logstore.DailyReportSnapshot) (string, string) {
	location, err := time.LoadLocation(snapshot.Timezone)
	if err != nil {
		return snapshot.WindowStart.Format(time.RFC3339), snapshot.WindowEnd.Format(time.RFC3339)
	}
	return snapshot.WindowStart.In(location).Format("2006-01-02 15:04 MST"), snapshot.WindowEnd.In(location).Format("2006-01-02 15:04 MST")
}

func takeDailyProviders(rows []logstore.DailyProviderReportRow, limit int) []logstore.DailyProviderReportRow {
	if len(rows) <= limit {
		return rows
	}
	return rows[:limit]
}

func isLikelyUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "unique index") ||
		strings.Contains(msg, "unique_violation") ||
		strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "duplicate entry") ||
		strings.Contains(msg, "duplicated key")
}

func initialDailyAudienceStatus(enabled bool, channelIDs []string) logstore.DailyReportAudienceStatus {
	switch {
	case !enabled:
		return logstore.DailyReportAudienceStatusNotEnabled
	case len(channelIDs) == 0:
		return logstore.DailyReportAudienceStatusNoChannels
	default:
		return logstore.DailyReportAudienceStatusPending
	}
}

func collapseDailyPendingStatus(status logstore.DailyReportAudienceStatus) logstore.DailyReportAudienceStatus {
	if status == logstore.DailyReportAudienceStatusPending {
		return logstore.DailyReportAudienceStatusFailed
	}
	return status
}

func overallDailyRunStatus(internal, external logstore.DailyReportAudienceStatus) logstore.DailyReportRunStatus {
	delivered := 0
	failed := 0
	pending := 0
	for _, value := range []logstore.DailyReportAudienceStatus{internal, external} {
		switch value {
		case logstore.DailyReportAudienceStatusDelivered:
			delivered++
		case logstore.DailyReportAudienceStatusFailed:
			failed++
		case logstore.DailyReportAudienceStatusPending:
			pending++
		}
	}
	switch {
	case pending > 0:
		return logstore.DailyReportRunStatusRunning
	case failed == 0:
		return logstore.DailyReportRunStatusSuccess
	case delivered > 0:
		return logstore.DailyReportRunStatusPartial
	default:
		return logstore.DailyReportRunStatusFailed
	}
}

func deriveDailyReportCurrentStatus(detail *DailyReportRunDetail) {
	if detail == nil {
		return
	}
	detail.CurrentInternalStatus = deriveDailyReportAudienceStatus(
		detail.Run.InternalStatus,
		logstore.DailyReportAudienceInternal,
		detail.Run.InternalChannelIDs,
		detail.Deliveries,
	)
	detail.CurrentExternalStatus = deriveDailyReportAudienceStatus(
		detail.Run.ExternalStatus,
		logstore.DailyReportAudienceExternal,
		detail.Run.ExternalChannelIDs,
		detail.Deliveries,
	)
	if detail.Run.Status == logstore.DailyReportRunStatusPrepared && len(detail.Deliveries) == 0 {
		detail.CurrentStatus = logstore.DailyReportRunStatusPrepared
		return
	}
	detail.CurrentStatus = overallDailyRunStatus(detail.CurrentInternalStatus, detail.CurrentExternalStatus)
}

func deriveDailyReportAudienceStatus(
	persisted logstore.DailyReportAudienceStatus,
	audience logstore.DailyReportAudience,
	channelIDs []string,
	deliveries []logstore.DailyReportDelivery,
) logstore.DailyReportAudienceStatus {
	if persisted == logstore.DailyReportAudienceStatusNotEnabled || len(channelIDs) == 0 {
		return persisted
	}
	latest := make(map[string]logstore.DailyReportDelivery, len(channelIDs))
	for _, delivery := range deliveries {
		if delivery.Audience != audience {
			continue
		}
		previous, ok := latest[delivery.ChannelID]
		if !ok || delivery.AttemptNo > previous.AttemptNo {
			latest[delivery.ChannelID] = delivery
		}
	}
	if len(latest) == 0 {
		return persisted
	}
	for _, channelID := range channelIDs {
		delivery, ok := latest[channelID]
		if !ok {
			return logstore.DailyReportAudienceStatusPending
		}
		if delivery.Status != logstore.DailyReportDeliveryStatusDelivered {
			return logstore.DailyReportAudienceStatusFailed
		}
	}
	return logstore.DailyReportAudienceStatusDelivered
}
