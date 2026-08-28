import { describe, expect, it } from "vitest";
import { channelRequest, durationFromSeconds, durationToSeconds, ruleRequest, safeHistoryDetail } from "./alertingModel";

describe("alerting model", () => {
	it("converts window and cooldown durations", () => {
		expect(durationToSeconds("5", "minutes")).toBe(300);
		expect(durationFromSeconds(7200)).toEqual({ value: "2", unit: "hours" });
		expect(durationFromSeconds(0, true)).toEqual({ value: "0", unit: "minutes" });
	});
	it("never resubmits redacted secrets", () => {
		expect(
			channelRequest({ name: "Slack", description: "", type: "slack", destination: "***redacted***", headers: "", enabled: true }, true)
				.config,
		).toEqual({});
	});
	it("validates reliability windows, channels and minimum samples", () => {
		expect(() =>
			ruleRequest({
				name: "errors",
				description: "",
				enabled: true,
				scope_type: "provider",
				scope_id: "openai",
				cel_expression: "provider_error_rate > 0.1",
				channel_ids: [],
				cooldown_seconds: 60,
				window_seconds: 300,
				min_requests: 10,
				notify_once_per_reset_cycle: false,
			}),
		).toThrow("channel");
	});
	it("pairs optional targets with the type allowed by each scope", () => {
		const base = {
			name: "targeted",
			description: "",
			enabled: true,
			scope_id: "scope",
			cel_expression: "true",
			channel_ids: ["channel"],
			cooldown_seconds: 60,
			window_seconds: 300,
			min_requests: 1,
			notify_once_per_reset_cycle: false,
		};
		expect(ruleRequest({ ...base, scope_type: "provider", target_id: "gpt-4o" })).toMatchObject({
			target_type: "model",
			target_id: "gpt-4o",
		});
		expect(ruleRequest({ ...base, scope_type: "team", target_id: "budget-1" })).toMatchObject({
			target_type: "budget",
			target_id: "budget-1",
		});
		expect(ruleRequest({ ...base, scope_type: "provider", target_id: "" })).toMatchObject({ target_type: undefined, target_id: undefined });
	});
	it("never exposes raw delivery failure details", () => expect(safeHistoryDetail("https://secret.example/hook?token=abc")).toBe("hidden"));
});