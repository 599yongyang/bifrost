import { describe, expect, it } from "vitest";
import type { DailyReportJobStatus, DailyReportPreview } from "@/lib/types/alerting";
import {
	dailyReportPermissions,
	externalPreviewProjection,
	formatDailyReportPercent,
	isDailyReportPreview,
	serializeDailyReportPreviewSettings,
	serializeDailyReportSettings,
	shouldPollDailyReportJob,
} from "./dailyReportModel";

const form = {
	enabled: true,
	timezone: "Asia/Shanghai",
	generate_time: "03:00",
	send_time: "09:00",
	slow_threshold_ms: 10000,
	internal_enabled: true,
	internal_channel_ids: ["a", "a"],
	external_enabled: true,
	external_channel_ids: ["b"],
};
describe("daily report UI model", () => {
	it("serializes settings and deduplicates audience channels", () =>
		expect(serializeDailyReportSettings(form).internal_channel_ids).toEqual(["a"]));
	it("accepts a zero slow threshold in persisted settings", () =>
		expect(serializeDailyReportSettings({ ...form, slow_threshold_ms: 0 }).slow_threshold_ms).toBe(0));
	it("allows preview without channels while persisted settings stay strict", () => {
		const noChannels = { ...form, internal_channel_ids: [], external_channel_ids: [] };
		expect(serializeDailyReportPreviewSettings(noChannels).internal_channel_ids).toEqual([]);
		expect(() => serializeDailyReportSettings(noChannels)).toThrow("channel");
	});
	it("renders backend percentage values without multiplying", () => expect(formatDailyReportPercent(98.75)).toBe("98.8%"));
	it("gates every mutation behind Governance View and Update", () => {
		expect(dailyReportPermissions(true, false)).toEqual({
			canView: true,
			canUpdate: false,
			canPreview: false,
			canGenerate: false,
			canResend: false,
		});
		expect(dailyReportPermissions(true, true).canGenerate).toBe(true);
	});
	it("recognizes 202 jobs and polls only active states", () => {
		const job = { id: "job", status: "pending" } as DailyReportJobStatus;
		expect(isDailyReportPreview(job)).toBe(false);
		expect(shouldPollDailyReportJob(job)).toBe(true);
		expect(shouldPollDailyReportJob({ status: "completed" })).toBe(false);
	});
	it("external preview is an explicit allowlist without provider/model/internal metrics", () => {
		const trend = { current: 1, previous: 0, delta: 1, delta_percentage: 100 };
		const preview = {
			business_date: "2026-08-28",
			external_content: "safe",
			internal_content: "secret",
			settings: {},
			snapshot: {
				business_date: "2026-08-28",
				timezone: "UTC",
				window_start: "",
				window_end: "",
				generated_at: "",
				slow_threshold_ms: 1,
				overview: {
					user_requests: 1,
					provider_attempts: 9,
					system_success_rate: 0.5,
					user_success_rate: 1,
					fallback_recoveries: 0,
					fallback_recovery_rate: 0,
					retry_count: 0,
					slow_requests: 0,
					slow_request_rate: 0,
					average_latency_ms: 1,
					p95_latency_ms: 1,
					p99_latency_ms: 1,
				},
				providers: [
					{
						provider: "secret-provider",
						attempts: 1,
						success_count: 1,
						success_rate: 1,
						retry_count: 0,
						slow_requests: 0,
						slow_request_rate: 0,
						average_latency_ms: 1,
						p95_latency_ms: 1,
						p99_latency_ms: 1,
					},
				],
				trends: {
					user_requests: trend,
					user_success_rate: trend,
					system_success_rate: trend,
					fallback_recoveries: trend,
					slow_request_rate: trend,
					average_latency_ms: trend,
					p95_latency_ms: trend,
					p99_latency_ms: trend,
				},
			},
		} as DailyReportPreview;
		const serialized = JSON.stringify(externalPreviewProjection(preview));
		expect(serialized).not.toContain("secret-provider");
		expect(serialized).not.toContain("provider_attempts");
		expect(serialized).not.toContain("system_success_rate");
		expect(serialized).not.toContain("secret");
	});
});