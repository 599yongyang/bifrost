import { describe, expect, it } from "vitest";
import type { DailyReportRunDetail } from "@/lib/types/alerting";
import { summarizeDailyReportAudience } from "./dailyReportPresentation";

function detail(overrides: Partial<DailyReportRunDetail> = {}): DailyReportRunDetail {
	return {
		run: {
			internal_channel_ids: ["internal"],
			external_channel_ids: ["external"],
		} as DailyReportRunDetail["run"],
		deliveries: [],
		current_status: "failed",
		current_internal_status: "failed",
		current_external_status: "failed",
		...overrides,
	};
}

describe("summarizeDailyReportAudience", () => {
	it("shows generation failure instead of pending when no delivery exists", () => {
		expect(summarizeDailyReportAudience(detail(), "internal")).toEqual({ kind: "status", status: "failed" });
	});

	it("uses only the latest attempt for each channel", () => {
		const result = summarizeDailyReportAudience(
			detail({
				deliveries: [
					{
						id: "1",
						run_id: "run",
						channel_id: "internal",
						audience: "internal",
						attempt_no: 1,
						status: "failed",
						created_at: "2026-08-27T00:00:00Z",
					},
					{
						id: "2",
						run_id: "run",
						channel_id: "internal",
						audience: "internal",
						attempt_no: 2,
						status: "delivered",
						created_at: "2026-08-27T00:01:00Z",
					},
				],
			}),
			"internal",
		);
		expect(result).toEqual({ kind: "deliveries", delivered: 1, failed: 0, total: 1 });
	});
});