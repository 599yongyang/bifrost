import { describe, expect, it } from "vitest";
import { alertMetricsForScope } from "./alertingRuleFields";

describe("alert rule metric visibility", () => {
	it("shows provider failure metrics for provider rules", () => {
		const names = alertMetricsForScope("provider").map(([name]) => name);
		expect(names).toEqual(["provider_error_rate", "provider_error_count", "provider_success_count", "provider_request_count"]);
	});

	it("keeps governance rules limited to governance metrics", () => {
		const names = alertMetricsForScope("virtual_key").map(([name]) => name);
		expect(names).toContain("budget_usage_percent");
		expect(names).not.toContain("provider_error_rate");
	});
});