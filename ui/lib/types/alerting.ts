export type AlertChannelType = "slack" | "microsoft_teams" | "wecom" | "pagerduty" | "webhook";
export type AlertScopeType = "virtual_key" | "team" | "customer" | "provider";
export type AlertStatus = "sent" | "failed" | "skipped";
export type DailyReportAudience = "internal" | "external";
export type DailyReportDeliveryStatus = "delivered" | "failed";
export type DailyReportRunStatus = "running" | "prepared" | "success" | "partial" | "failed";
export type DailyReportAudienceStatus = "pending" | "delivered" | "failed" | "not_enabled" | "no_channels";

export interface AlertChannel {
	id: string;
	name: string;
	description?: string;
	type: AlertChannelType;
	enabled: boolean;
	config: Record<string, unknown>;
	created_at: string;
	updated_at: string;
}

export type AlertChannelRequest = Omit<AlertChannel, "id" | "created_at" | "updated_at">;

export interface AlertRule {
	id: string;
	name: string;
	description?: string;
	enabled: boolean;
	scope_type: AlertScopeType;
	scope_id: string;
	target_type?: "budget" | "model";
	target_id?: string;
	cel_expression: string;
	channel_ids: string[];
	query?: Record<string, unknown>;
	cooldown_milliseconds: number;
	window_seconds: number;
	min_requests: number;
	notify_once_per_reset_cycle: boolean;
	created_at: string;
	updated_at: string;
}

export type AlertRuleRequest = Omit<AlertRule, "id" | "created_at" | "updated_at">;

export interface AlertRuleEvaluationResult {
	rule_id: string;
	matched: boolean;
	matched_targets: number;
	sent_count: number;
	skipped_count: number;
	failed_count: number;
	cooldown_ignored: boolean;
}

export interface AlertHistoryRecord {
	id: string;
	rule_id: string;
	rule_name: string;
	channel_id?: string;
	channel_name?: string;
	channel_type?: AlertChannelType;
	scope_type: AlertScopeType;
	scope_id: string;
	target_type?: string;
	target_id?: string;
	cel_expression: string;
	input: Record<string, unknown>;
	status: AlertStatus;
	status_detail?: string;
	created_at: string;
}

export interface AlertHistoryParams {
	limit?: number;
	offset?: number;
	status?: AlertStatus[];
	scope_type?: AlertScopeType[];
	channel_type?: AlertChannelType[];
}

export interface DailyReportSettings {
	id: string;
	enabled: boolean;
	timezone: string;
	generate_time: string;
	send_time: string;
	slow_threshold_ms: number;
	internal_enabled: boolean;
	internal_channel_ids: string[];
	external_enabled: boolean;
	external_channel_ids: string[];
	created_at?: string;
	updated_at?: string;
}

export type DailyReportSettingsRequest = Partial<DailyReportSettings>;

export interface DailyReportHistoryParams {
	limit?: number;
	offset?: number;
	audience?: DailyReportAudience[];
}

export interface DailyReportErrorBucket {
	key: string;
	label: string;
	count: number;
	rate: number;
	description?: string;
}

export interface DailyReportTrendValue {
	current: number;
	previous: number;
	delta: number;
	delta_percentage: number;
}

export interface DailyReportOverview {
	user_requests: number;
	provider_attempts: number;
	system_success_rate: number;
	user_success_rate: number;
	fallback_recoveries: number;
	fallback_recovery_rate: number;
	retry_count: number;
	slow_requests: number;
	slow_request_rate: number;
	average_latency_ms: number;
	p95_latency_ms: number;
	p99_latency_ms: number;
	error_buckets?: DailyReportErrorBucket[];
}

export interface DailyModelReportRow {
	provider: string;
	model: string;
	attempts: number;
	success_count: number;
	success_rate: number;
	retry_count: number;
	slow_requests: number;
	slow_request_rate: number;
	average_latency_ms: number;
	p95_latency_ms: number;
	p99_latency_ms: number;
	error_buckets?: DailyReportErrorBucket[];
}

export interface DailyProviderReportRow {
	provider: string;
	attempts: number;
	success_count: number;
	success_rate: number;
	retry_count: number;
	slow_requests: number;
	slow_request_rate: number;
	average_latency_ms: number;
	p95_latency_ms: number;
	p99_latency_ms: number;
	error_buckets?: DailyReportErrorBucket[];
	models?: DailyModelReportRow[];
}

export interface DailyReportTrends {
	user_requests: DailyReportTrendValue;
	user_success_rate: DailyReportTrendValue;
	system_success_rate: DailyReportTrendValue;
	fallback_recoveries: DailyReportTrendValue;
	slow_request_rate: DailyReportTrendValue;
	average_latency_ms: DailyReportTrendValue;
	p95_latency_ms: DailyReportTrendValue;
	p99_latency_ms: DailyReportTrendValue;
}

export type PublicDailyReportTrends = Omit<DailyReportTrends, "system_success_rate">;

export interface DailyReportSnapshot {
	business_date: string;
	timezone: string;
	window_start: string;
	window_end: string;
	generated_at: string;
	slow_threshold_ms: number;
	overview: DailyReportOverview;
	providers: DailyProviderReportRow[];
	trends: DailyReportTrends;
}

export interface PublicDailyReportSnapshot {
	business_date: string;
	timezone: string;
	window_start: string;
	window_end: string;
	generated_at: string;
	slow_threshold_ms: number;
	user_requests: number;
	user_success_rate: number;
	fallback_recoveries: number;
	fallback_recovery_rate: number;
	slow_requests: number;
	slow_request_rate: number;
	average_latency_ms: number;
	p95_latency_ms: number;
	p99_latency_ms: number;
	error_buckets?: DailyReportErrorBucket[];
	trends: PublicDailyReportTrends;
}

export interface DailyReportPreview {
	business_date: string;
	settings: DailyReportSettings;
	snapshot: DailyReportSnapshot;
	internal_content: string;
	external_content: string;
}

export interface DailyReportRun {
	id: string;
	business_date: string;
	timezone: string;
	window_start: string;
	window_end: string;
	slow_threshold_ms: number;
	trigger: string;
	status: DailyReportRunStatus;
	internal_status: DailyReportAudienceStatus;
	external_status: DailyReportAudienceStatus;
	internal_status_detail?: string;
	external_status_detail?: string;
	generated_at: string;
	internal_content: string;
	external_content: string;
	internal_channel_ids: string[];
	external_channel_ids: string[];
	created_at: string;
	snapshot: DailyReportSnapshot;
	public_snapshot: PublicDailyReportSnapshot;
}

export interface DailyReportDelivery {
	id: string;
	run_id: string;
	audience: DailyReportAudience;
	channel_id: string;
	channel_name?: string;
	channel_type?: AlertChannelType;
	attempt_no: number;
	status: DailyReportDeliveryStatus;
	status_detail?: string;
	created_at: string;
}

export interface DailyReportRunDetail {
	run: DailyReportRun;
	deliveries: DailyReportDelivery[];
	current_status: DailyReportRunStatus;
	current_internal_status: DailyReportAudienceStatus;
	current_external_status: DailyReportAudienceStatus;
}