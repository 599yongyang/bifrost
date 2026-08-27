package logstore

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/maximhq/bifrost/core/schemas"
	"gorm.io/gorm"
)

const (
	dailyReportChunkSize          = 500
	dailyReportLatencySampleLimit = 10000
	dailyReportBatchYield         = 2 * time.Millisecond
)

type dailyAttemptProjection struct {
	Timestamp       time.Time `gorm:"column:timestamp"`
	Provider        string    `gorm:"column:provider"`
	Model           string    `gorm:"column:model"`
	Status          string    `gorm:"column:status"`
	Latency         *float64  `gorm:"column:latency"`
	NumberOfRetries int       `gorm:"column:number_of_retries"`
	ErrorDetails    string    `gorm:"column:error_details"`
}

type dailyRootProjection struct {
	ID        string    `gorm:"column:id"`
	Timestamp time.Time `gorm:"column:timestamp"`
	Status    string    `gorm:"column:status"`
	Latency   *float64  `gorm:"column:latency"`
}

type dailyChildProjection struct {
	ParentRequestID string   `gorm:"column:parent_request_id"`
	Status          string   `gorm:"column:status"`
	Latency         *float64 `gorm:"column:latency"`
}

type rootState struct {
	success            bool
	hasFallbackSuccess bool
	latency            float64
	hasLatency         bool
}

type dailyAggregate struct {
	attempts     int64
	successes    int64
	retries      int64
	slow         int64
	latency      dailyLatencyStats
	errorBuckets map[string]int64
}

type dailyRootAggregate struct {
	total              int64
	successes          int64
	fallbackRecoveries int64
	slow               int64
	latency            dailyLatencyStats
}

// dailyLatencyStats keeps an exact count/average and only a bounded sample for
// percentiles. Report generation must not retain one float per daily request.
type dailyLatencyStats struct {
	count  int64
	sum    float64
	sample []float64
}

func (s *dailyLatencyStats) Add(value float64) {
	s.count++
	s.sum += value
	if len(s.sample) < dailyReportLatencySampleLimit {
		s.sample = append(s.sample, value)
		return
	}
	// Deterministic reservoir sampling avoids shared RNG state and keeps tests
	// reproducible while bounding each aggregate's percentile memory.
	position := (uint64(s.count)*11400714819323198485 + 1442695040888963407) % uint64(s.count)
	if position < dailyReportLatencySampleLimit {
		s.sample[position] = value
	}
}

func (s dailyLatencyStats) Average() float64 {
	if s.count == 0 {
		return 0
	}
	return s.sum / float64(s.count)
}

func (s dailyLatencyStats) Percentile(value float64) float64 {
	return percentile(s.sample, value)
}

type dailyWindowAggregate struct {
	overview  dailyAggregate
	roots     dailyRootAggregate
	providers map[string]*dailyAggregate
	models    map[string]*dailyAggregate
}

func newDailyWindowAggregate() dailyWindowAggregate {
	return dailyWindowAggregate{
		providers: make(map[string]*dailyAggregate),
		models:    make(map[string]*dailyAggregate),
	}
}

func (s *RDBLogStore) BuildDailyReportSnapshot(ctx context.Context, query DailyReportMetricsQuery) (*DailyReportSnapshot, error) {
	currentStart := query.WindowStart.UTC()
	currentEnd := query.WindowEnd.UTC()
	if !currentEnd.After(currentStart) {
		return nil, fmt.Errorf("daily report window end must be after start")
	}
	prevStart, err := previousDailyReportWindowStart(query)
	if err != nil {
		return nil, err
	}

	current := newDailyWindowAggregate()
	previous := newDailyWindowAggregate()

	if err := s.collectDailyAttemptAggregates(ctx, prevStart, currentEnd, currentStart, query.SlowThresholdMs, &current, &previous); err != nil {
		return nil, err
	}
	if err := s.collectDailyRootAggregates(ctx, prevStart, currentEnd, currentStart, query.SlowThresholdMs, &current, &previous); err != nil {
		return nil, err
	}

	snapshot := &DailyReportSnapshot{
		BusinessDate:    query.BusinessDate,
		Timezone:        query.Timezone,
		WindowStart:     currentStart,
		WindowEnd:       currentEnd,
		GeneratedAt:     query.GeneratedAt.UTC(),
		SlowThresholdMs: query.SlowThresholdMs,
		Overview:        buildDailyOverview(current.overview, current.roots),
		Providers:       buildDailyProviderRows(current),
		Trends:          buildDailyTrends(buildDailyOverview(current.overview, current.roots), buildDailyOverview(previous.overview, previous.roots)),
	}
	return snapshot, nil
}

func previousDailyReportWindowStart(query DailyReportMetricsQuery) (time.Time, error) {
	location, err := time.LoadLocation(query.Timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid daily report timezone %q: %w", query.Timezone, err)
	}
	day, err := time.ParseInLocation("2006-01-02", query.BusinessDate, location)
	if err != nil {
		return time.Time{}, fmt.Errorf("daily report business date must use YYYY-MM-DD: %w", err)
	}
	// Calendar arithmetic preserves local-midnight boundaries across 23/25-hour
	// DST days; subtracting the current window duration does not.
	return day.AddDate(0, 0, -1).UTC(), nil
}

func (s *RDBLogStore) collectDailyAttemptAggregates(
	ctx context.Context,
	start time.Time,
	end time.Time,
	currentStart time.Time,
	slowThresholdMs int64,
	current *dailyWindowAggregate,
	previous *dailyWindowAggregate,
) error {
	base := s.ScopedDB(ctx).Model(&Log{}).
		Select("timestamp, provider, model, status, latency, number_of_retries, error_details").
		Where("timestamp >= ? AND timestamp < ?", start, end).
		Where("status IN ?", []string{"success", "error"})
	return base.FindInBatches(&[]dailyAttemptProjection{}, dailyReportChunkSize, func(tx *gorm.DB, _ int) error {
		rows, ok := tx.Statement.Dest.(*[]dailyAttemptProjection)
		if !ok {
			return fmt.Errorf("unexpected batch destination type %T", tx.Statement.Dest)
		}
		for _, row := range *rows {
			target := current
			if row.Timestamp.Before(currentStart) {
				target = previous
			}
			addDailyAttempt(target, row, slowThresholdMs)
		}
		return yieldDailyReportBatch(ctx)
	}).Error
}

func (s *RDBLogStore) collectDailyRootAggregates(
	ctx context.Context,
	start time.Time,
	end time.Time,
	currentStart time.Time,
	slowThresholdMs int64,
	current *dailyWindowAggregate,
	previous *dailyWindowAggregate,
) error {
	base := s.ScopedDB(ctx).Model(&Log{}).
		Select("id, timestamp, status, latency").
		Where("timestamp >= ? AND timestamp < ?", start, end).
		Where("fallback_index = ?", 0).
		Where("status IN ?", []string{"success", "error"})
	return base.FindInBatches(&[]dailyRootProjection{}, dailyReportChunkSize, func(tx *gorm.DB, _ int) error {
		rows, ok := tx.Statement.Dest.(*[]dailyRootProjection)
		if !ok {
			return fmt.Errorf("unexpected root batch destination type %T", tx.Statement.Dest)
		}
		// Resolve fallback children while this bounded batch is in memory. Keeping
		// every root ID for a full day would make peak memory scale linearly with
		// traffic volume and could affect the gateway process.
		currentRoots := make(map[string]*rootState, len(*rows))
		previousRoots := make(map[string]*rootState, len(*rows))
		for _, row := range *rows {
			state := &rootState{success: row.Status == "success"}
			if row.Latency != nil && *row.Latency >= 0 {
				state.latency = *row.Latency
				state.hasLatency = true
			}
			if row.Timestamp.Before(currentStart) {
				previousRoots[row.ID] = state
				previous.roots.total++
				if state.success {
					previous.roots.successes++
				}
				continue
			}
			currentRoots[row.ID] = state
			current.roots.total++
			if state.success {
				current.roots.successes++
			}
		}
		if err := s.applyFallbackSuccesses(ctx, currentRoots, slowThresholdMs, &current.roots); err != nil {
			return err
		}
		if err := s.applyFallbackSuccesses(ctx, previousRoots, slowThresholdMs, &previous.roots); err != nil {
			return err
		}
		return yieldDailyReportBatch(ctx)
	}).Error
}

func yieldDailyReportBatch(ctx context.Context) error {
	timer := time.NewTimer(dailyReportBatchYield)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *RDBLogStore) CreateDailyReportRun(ctx context.Context, run *DailyReportRun) error {
	return s.db.WithContext(ctx).Create(run).Error
}

func (s *RDBLogStore) UpdateDailyReportRun(ctx context.Context, id string, updates map[string]interface{}) error {
	result := s.db.WithContext(ctx).Model(&DailyReportRun{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *RDBLogStore) FindDailyReportRun(ctx context.Context, id string) (*DailyReportRun, error) {
	var run DailyReportRun
	if err := s.db.WithContext(ctx).Where("id = ?", id).First(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &run, nil
}

func (s *RDBLogStore) FindDailyReportRunByBusinessDate(ctx context.Context, businessDate, timezone string) (*DailyReportRun, error) {
	var run DailyReportRun
	if err := s.db.WithContext(ctx).
		Where("business_date = ? AND timezone = ?", businessDate, timezone).
		First(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &run, nil
}

func (s *RDBLogStore) ListDailyReportRuns(ctx context.Context, query DailyReportHistoryQuery) ([]DailyReportRun, int64, error) {
	limit := query.Limit
	if limit <= 0 || limit > 200 {
		limit = 25
	}
	offset := query.Offset
	if offset < 0 {
		offset = 0
	}
	base := s.db.WithContext(ctx).Model(&DailyReportRun{})
	if len(query.Audiences) > 0 {
		values := make([]string, 0, len(query.Audiences))
		for _, audience := range query.Audiences {
			values = append(values, string(audience))
		}
		base = base.Where("id IN (?)",
			s.db.WithContext(ctx).Model(&DailyReportDelivery{}).Select("DISTINCT run_id").Where("audience IN ?", values),
		)
	}
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var runs []DailyReportRun
	err := base.Order("business_date DESC, created_at DESC, id DESC").Limit(limit).Offset(offset).Find(&runs).Error
	return runs, total, err
}

func (s *RDBLogStore) CreateDailyReportDelivery(ctx context.Context, delivery *DailyReportDelivery) error {
	return s.db.WithContext(ctx).Create(delivery).Error
}

func (s *RDBLogStore) ListDailyReportDeliveries(ctx context.Context, runID string) ([]DailyReportDelivery, error) {
	var deliveries []DailyReportDelivery
	err := s.db.WithContext(ctx).
		Where("run_id = ?", runID).
		Order("created_at DESC, attempt_no DESC, id DESC").
		Find(&deliveries).Error
	return deliveries, err
}

func (h *HybridLogStore) BuildDailyReportSnapshot(ctx context.Context, query DailyReportMetricsQuery) (*DailyReportSnapshot, error) {
	return h.inner.BuildDailyReportSnapshot(ctx, query)
}

func (h *HybridLogStore) CreateDailyReportRun(ctx context.Context, run *DailyReportRun) error {
	return h.inner.CreateDailyReportRun(ctx, run)
}

func (h *HybridLogStore) UpdateDailyReportRun(ctx context.Context, id string, updates map[string]interface{}) error {
	return h.inner.UpdateDailyReportRun(ctx, id, updates)
}

func (h *HybridLogStore) FindDailyReportRun(ctx context.Context, id string) (*DailyReportRun, error) {
	return h.inner.FindDailyReportRun(ctx, id)
}

func (h *HybridLogStore) FindDailyReportRunByBusinessDate(ctx context.Context, businessDate, timezone string) (*DailyReportRun, error) {
	return h.inner.FindDailyReportRunByBusinessDate(ctx, businessDate, timezone)
}

func (h *HybridLogStore) ListDailyReportRuns(ctx context.Context, query DailyReportHistoryQuery) ([]DailyReportRun, int64, error) {
	return h.inner.ListDailyReportRuns(ctx, query)
}

func (h *HybridLogStore) CreateDailyReportDelivery(ctx context.Context, delivery *DailyReportDelivery) error {
	return h.inner.CreateDailyReportDelivery(ctx, delivery)
}

func (h *HybridLogStore) ListDailyReportDeliveries(ctx context.Context, runID string) ([]DailyReportDelivery, error) {
	return h.inner.ListDailyReportDeliveries(ctx, runID)
}

func addDailyAttempt(target *dailyWindowAggregate, row dailyAttemptProjection, slowThresholdMs int64) {
	addDailyAggregate(&target.overview, row, slowThresholdMs)
	providerAgg := target.providers[row.Provider]
	if providerAgg == nil {
		providerAgg = &dailyAggregate{}
		target.providers[row.Provider] = providerAgg
	}
	addDailyAggregate(providerAgg, row, slowThresholdMs)
	modelKey := row.Provider + "\x00" + row.Model
	modelAgg := target.models[modelKey]
	if modelAgg == nil {
		modelAgg = &dailyAggregate{}
		target.models[modelKey] = modelAgg
	}
	addDailyAggregate(modelAgg, row, slowThresholdMs)
}

func addDailyAggregate(aggregate *dailyAggregate, row dailyAttemptProjection, slowThresholdMs int64) {
	aggregate.attempts++
	if row.Status == "success" {
		aggregate.successes++
	}
	aggregate.retries += int64(row.NumberOfRetries)
	if row.Latency != nil {
		latency := *row.Latency
		if latency >= 0 {
			aggregate.latency.Add(latency)
			if slowThresholdMs > 0 && latency >= float64(slowThresholdMs) {
				aggregate.slow++
			}
		}
	}
	if row.Status != "success" {
		if aggregate.errorBuckets == nil {
			aggregate.errorBuckets = make(map[string]int64)
		}
		aggregate.errorBuckets[classifyDailyError(row.ErrorDetails)]++
	}
}

func (s *RDBLogStore) applyFallbackSuccesses(ctx context.Context, roots map[string]*rootState, slowThresholdMs int64, aggregate *dailyRootAggregate) error {
	if len(roots) == 0 {
		return nil
	}
	rootIDs := make([]string, 0, len(roots))
	for id := range roots {
		rootIDs = append(rootIDs, id)
	}
	for start := 0; start < len(rootIDs); start += dailyReportChunkSize {
		end := start + dailyReportChunkSize
		if end > len(rootIDs) {
			end = len(rootIDs)
		}
		var children []dailyChildProjection
		if err := s.ScopedDB(ctx).Model(&Log{}).
			Select("parent_request_id, status, latency").
			Where("parent_request_id IN ?", rootIDs[start:end]).
			Where("fallback_index > 0").
			Where("status IN ?", []string{"success", "error"}).
			Find(&children).Error; err != nil {
			return err
		}
		for _, child := range children {
			state := roots[child.ParentRequestID]
			if state == nil {
				continue
			}
			if child.Status == "success" {
				state.hasFallbackSuccess = true
			}
			if child.Latency != nil && *child.Latency >= 0 {
				state.latency += *child.Latency
				state.hasLatency = true
			}
		}
	}
	for _, state := range roots {
		if state.success || state.hasFallbackSuccess {
			if !state.success {
				aggregate.successes++
			}
		}
		if !state.success && state.hasFallbackSuccess {
			aggregate.fallbackRecoveries++
		}
		if state.hasLatency {
			aggregate.latency.Add(state.latency)
			if slowThresholdMs > 0 && state.latency >= float64(slowThresholdMs) {
				aggregate.slow++
			}
		}
	}
	return nil
}

func buildDailyOverview(agg dailyAggregate, roots dailyRootAggregate) DailyReportOverview {
	return DailyReportOverview{
		UserRequests:         roots.total,
		ProviderAttempts:     agg.attempts,
		SystemSuccessRate:    ratioPct(agg.successes, agg.attempts),
		UserSuccessRate:      ratioPct(roots.successes, roots.total),
		FallbackRecoveries:   roots.fallbackRecoveries,
		FallbackRecoveryRate: ratioPct(roots.fallbackRecoveries, roots.total),
		RetryCount:           agg.retries,
		SlowRequests:         roots.slow,
		SlowRequestRate:      ratioPct(roots.slow, roots.total),
		AverageLatencyMs:     roots.latency.Average(),
		P95LatencyMs:         roots.latency.Percentile(95),
		P99LatencyMs:         roots.latency.Percentile(99),
		ErrorBuckets:         materializeErrorBuckets(agg.errorBuckets, agg.attempts-agg.successes),
	}
}

func buildDailyProviderRows(window dailyWindowAggregate) []DailyProviderReportRow {
	providerNames := make([]string, 0, len(window.providers))
	for provider := range window.providers {
		providerNames = append(providerNames, provider)
	}
	sort.Strings(providerNames)
	rows := make([]DailyProviderReportRow, 0, len(providerNames))
	for _, provider := range providerNames {
		agg := window.providers[provider]
		row := DailyProviderReportRow{
			Provider:         provider,
			Attempts:         agg.attempts,
			SuccessCount:     agg.successes,
			SuccessRate:      ratioPct(agg.successes, agg.attempts),
			RetryCount:       agg.retries,
			SlowRequests:     agg.slow,
			SlowRequestRate:  ratioPct(agg.slow, agg.attempts),
			AverageLatencyMs: agg.latency.Average(),
			P95LatencyMs:     agg.latency.Percentile(95),
			P99LatencyMs:     agg.latency.Percentile(99),
			ErrorBuckets:     materializeErrorBuckets(agg.errorBuckets, agg.attempts-agg.successes),
			Models:           buildDailyModelRows(provider, window.models),
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Attempts == rows[j].Attempts {
			return rows[i].Provider < rows[j].Provider
		}
		return rows[i].Attempts > rows[j].Attempts
	})
	return rows
}

func buildDailyModelRows(provider string, models map[string]*dailyAggregate) []DailyModelReportRow {
	rows := make([]DailyModelReportRow, 0)
	prefix := provider + "\x00"
	for key, agg := range models {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		model := strings.TrimPrefix(key, prefix)
		rows = append(rows, DailyModelReportRow{
			Provider:         provider,
			Model:            model,
			Attempts:         agg.attempts,
			SuccessCount:     agg.successes,
			SuccessRate:      ratioPct(agg.successes, agg.attempts),
			RetryCount:       agg.retries,
			SlowRequests:     agg.slow,
			SlowRequestRate:  ratioPct(agg.slow, agg.attempts),
			AverageLatencyMs: agg.latency.Average(),
			P95LatencyMs:     agg.latency.Percentile(95),
			P99LatencyMs:     agg.latency.Percentile(99),
			ErrorBuckets:     materializeErrorBuckets(agg.errorBuckets, agg.attempts-agg.successes),
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Attempts == rows[j].Attempts {
			return rows[i].Model < rows[j].Model
		}
		return rows[i].Attempts > rows[j].Attempts
	})
	return rows
}

func buildDailyTrends(current DailyReportOverview, previous DailyReportOverview) DailyReportTrends {
	return DailyReportTrends{
		UserRequests:       trendValue(float64(current.UserRequests), float64(previous.UserRequests)),
		UserSuccessRate:    trendValue(current.UserSuccessRate, previous.UserSuccessRate),
		SystemSuccessRate:  trendValue(current.SystemSuccessRate, previous.SystemSuccessRate),
		FallbackRecoveries: trendValue(float64(current.FallbackRecoveries), float64(previous.FallbackRecoveries)),
		SlowRequestRate:    trendValue(current.SlowRequestRate, previous.SlowRequestRate),
		AverageLatencyMs:   trendValue(current.AverageLatencyMs, previous.AverageLatencyMs),
		P95LatencyMs:       trendValue(current.P95LatencyMs, previous.P95LatencyMs),
		P99LatencyMs:       trendValue(current.P99LatencyMs, previous.P99LatencyMs),
	}
}

func trendValue(current, previous float64) DailyReportTrendValue {
	delta := current - previous
	return DailyReportTrendValue{
		Current:         current,
		Previous:        previous,
		Delta:           delta,
		DeltaPercentage: pctChangeFloat(previous, current),
	}
}

func classifyDailyError(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "other"
	}
	var bifrostErr schemas.BifrostError
	if err := sonic.Unmarshal([]byte(raw), &bifrostErr); err != nil {
		return "other"
	}
	if bifrostErr.StatusCode != nil && *bifrostErr.StatusCode == 429 {
		return "rate_limited"
	}
	if bifrostErr.ExtraFields.TimeoutSource != "" {
		return "timeout_or_network"
	}
	if bifrostErr.Error != nil && bifrostErr.Error.Type != nil {
		switch *bifrostErr.Error.Type {
		case schemas.RequestTimedOut, schemas.ProviderConnectionFailed:
			return "timeout_or_network"
		}
	}
	if bifrostErr.StatusCode != nil {
		switch {
		case *bifrostErr.StatusCode >= 500:
			return "provider_5xx"
		case *bifrostErr.StatusCode >= 400:
			return "other_4xx"
		}
	}
	return "other"
}

func materializeErrorBuckets(raw map[string]int64, total int64) []DailyReportErrorBucket {
	keys := []string{"rate_limited", "other_4xx", "provider_5xx", "timeout_or_network", "other"}
	labels := map[string]string{
		"rate_limited":       "限流（429）",
		"other_4xx":          "其他客户端错误（4xx）",
		"provider_5xx":       "供应商服务错误（5xx）",
		"timeout_or_network": "超时或网络错误",
		"other":              "其他错误",
	}
	result := make([]DailyReportErrorBucket, 0, len(keys))
	for _, key := range keys {
		count := raw[key]
		if count == 0 {
			continue
		}
		result = append(result, DailyReportErrorBucket{
			Key:   key,
			Label: labels[key],
			Count: count,
			Rate:  ratioPct(count, total),
		})
	}
	return result
}

func ratioPct(numerator, denominator int64) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator) * 100
}

func average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, value := range values {
		sum += value
	}
	return sum / float64(len(values))
}

func percentile(values []float64, percentile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	if len(sorted) == 1 {
		return sorted[0]
	}
	rank := percentile / 100 * float64(len(sorted)-1)
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))
	if lower == upper {
		return sorted[lower]
	}
	weight := rank - float64(lower)
	return sorted[lower] + (sorted[upper]-sorted[lower])*weight
}

func pctChangeFloat(previous, current float64) float64 {
	if previous == 0 {
		if current == 0 {
			return 0
		}
		return 100
	}
	return (current - previous) / previous * 100
}
