import { describe, expect, it } from "vitest";
import { formatAlertCondition } from "./alertingCondition";

const labels: Record<string, string> = {
	providerErrorRate: "供应商错误率",
	providerErrorCount: "供应商请求失败数",
	providerRequestCount: "供应商请求总数",
	and: "且",
	or: "或",
};
const translate = (key: string) => labels[key] ?? key;

describe("formatAlertCondition", () => {
	it("formats a provider count condition with a unit", () => {
		expect(
			formatAlertCondition({ combinator: "and", rules: [{ field: "provider_error_count", operator: ">=", value: 3 }] }, translate),
		).toBe("供应商请求失败数 ≥ 3 次");
	});

	it("formats compound and nested conditions semantically", () => {
		expect(
			formatAlertCondition(
				{
					combinator: "and",
					rules: [
						{ field: "provider_request_count", operator: ">=", value: 100 },
						{
							combinator: "or",
							rules: [
								{ field: "provider_error_rate", operator: ">=", value: 30 },
								{ field: "provider_error_count", operator: ">", value: 10 },
							],
						},
					],
				},
				translate,
			),
		).toBe("供应商请求总数 ≥ 100 次 且（供应商错误率 ≥ 30% 或 供应商请求失败数 > 10 次）");
	});

	it("returns null when a hand-written or unknown condition cannot be represented safely", () => {
		expect(
			formatAlertCondition({ combinator: "and", rules: [{ field: "custom_metric", operator: ">=", value: 1 }] }, translate),
		).toBeNull();
		expect(formatAlertCondition(undefined, translate)).toBeNull();
	});
});