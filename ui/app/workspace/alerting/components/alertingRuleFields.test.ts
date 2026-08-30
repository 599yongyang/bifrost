import { describe, expect, it } from "vitest";
import { alertMetricsForScope } from "./alertingRuleFields";

describe("alert rule fields", () => {
	it("uses governance metrics for governance scopes", () => {
		expect(alertMetricsForScope("virtual_key").map(([name]) => name)).toContain("budget_usage_percent");
		expect(alertMetricsForScope("team").map(([name]) => name)).not.toContain("provider_error_rate");
	});

	it("uses provider reliability metrics for provider scopes", () => {
		expect(alertMetricsForScope("provider").map(([name]) => name)).toEqual([
			"provider_error_rate",
			"provider_error_count",
			"provider_success_count",
			"provider_request_count",
		]);
	});
});