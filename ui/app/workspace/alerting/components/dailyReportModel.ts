import type {
	DailyReportJobStatus,
	DailyReportPreview,
	DailyReportSettings,
	DailyReportSettingsRequest,
	PublicDailyReportSnapshot,
} from "@/lib/types/alerting";

export interface DailyReportSettingsForm {
	enabled: boolean;
	timezone: string;
	generate_time: string;
	send_time: string;
	slow_threshold_ms: number;
	internal_enabled: boolean;
	internal_channel_ids: string[];
	external_enabled: boolean;
	external_channel_ids: string[];
}

function validateDailyReportBase(form: DailyReportSettingsForm) {
	if (!form.timezone.trim()) throw new Error("Timezone is required");
	if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(form.generate_time) || !/^([01]\d|2[0-3]):[0-5]\d$/.test(form.send_time))
		throw new Error("Times must use HH:MM");
	if (!Number.isInteger(form.slow_threshold_ms) || form.slow_threshold_ms < 0)
		throw new Error("Slow threshold must be a non-negative whole number");
}

function normalizedDailyReportSettings(form: DailyReportSettingsForm): DailyReportSettingsRequest {
	return {
		...form,
		timezone: form.timezone.trim(),
		internal_channel_ids: [...new Set(form.internal_channel_ids)],
		external_channel_ids: [...new Set(form.external_channel_ids)],
	};
}

export function serializeDailyReportSettings(form: DailyReportSettingsForm): DailyReportSettingsRequest {
	validateDailyReportBase(form);
	if (form.internal_enabled && form.internal_channel_ids.length === 0) throw new Error("Select an internal audience channel");
	if (form.external_enabled && form.external_channel_ids.length === 0) throw new Error("Select an external audience channel");
	return normalizedDailyReportSettings(form);
}

export function serializeDailyReportPreviewSettings(form: DailyReportSettingsForm): DailyReportSettingsRequest {
	validateDailyReportBase(form);
	return normalizedDailyReportSettings(form);
}

export function settingsToForm(settings: DailyReportSettings): DailyReportSettingsForm {
	return {
		enabled: settings.enabled,
		timezone: settings.timezone,
		generate_time: settings.generate_time,
		send_time: settings.send_time,
		slow_threshold_ms: settings.slow_threshold_ms,
		internal_enabled: settings.internal_enabled,
		internal_channel_ids: settings.internal_channel_ids ?? [],
		external_enabled: settings.external_enabled,
		external_channel_ids: settings.external_channel_ids ?? [],
	};
}

export function isDailyReportPreview(
	value: { preview: DailyReportPreview } | DailyReportJobStatus,
): value is { preview: DailyReportPreview } {
	return "preview" in value;
}

export function shouldPollDailyReportJob(status?: DailyReportJobStatus) {
	return status?.status === "pending" || status?.status === "running";
}

// The backend already returns percentages in the 0..100 range.
export function formatDailyReportPercent(value: number) {
	return `${value.toFixed(1)}%`;
}

export function dailyReportPermissions(canView: boolean, canUpdate: boolean) {
	return {
		canView,
		canUpdate: canView && canUpdate,
		canPreview: canView && canUpdate,
		canGenerate: canView && canUpdate,
		canResend: canView && canUpdate,
	};
}

// Explicit customer-facing allowlist. Never spread the internal snapshot: that
// would silently expose providers/models or system success metrics when fields evolve.
export function externalPreviewProjection(preview: Pick<DailyReportPreview, "business_date" | "snapshot" | "external_content">): {
	business_date: string;
	external_content: string;
	snapshot: PublicDailyReportSnapshot;
} {
	const source = preview.snapshot;
	return {
		business_date: preview.business_date,
		external_content: preview.external_content,
		snapshot: {
			business_date: source.business_date,
			timezone: source.timezone,
			window_start: source.window_start,
			window_end: source.window_end,
			generated_at: source.generated_at,
			slow_threshold_ms: source.slow_threshold_ms,
			user_requests: source.overview.user_requests,
			user_success_rate: source.overview.user_success_rate,
			fallback_recoveries: source.overview.fallback_recoveries,
			fallback_recovery_rate: source.overview.fallback_recovery_rate,
			slow_requests: source.overview.slow_requests,
			slow_request_rate: source.overview.slow_request_rate,
			average_latency_ms: source.overview.average_latency_ms,
			p95_latency_ms: source.overview.p95_latency_ms,
			p99_latency_ms: source.overview.p99_latency_ms,
			error_buckets: source.overview.error_buckets,
			trends: {
				user_requests: source.trends.user_requests,
				user_success_rate: source.trends.user_success_rate,
				fallback_recoveries: source.trends.fallback_recoveries,
				slow_request_rate: source.trends.slow_request_rate,
				average_latency_ms: source.trends.average_latency_ms,
				p95_latency_ms: source.trends.p95_latency_ms,
				p99_latency_ms: source.trends.p99_latency_ms,
			},
		},
	};
}