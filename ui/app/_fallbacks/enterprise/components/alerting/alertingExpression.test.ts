import { describe, expect, it } from "vitest";
import { alertQueryToCEL, buildAlertCEL } from "./alertingExpression";

describe("alertQueryToCEL", () => {
	it.each([
		"provider_error_rate",
		"budget_usage_percent",
		"budget_spent",
		"rate_limit_request_usage_percent",
		"rate_limit_token_usage_percent",
	])("preserves CEL double literals for %s", (field) => {
		expect(
			alertQueryToCEL({
				combinator: "and",
				rules: [{ field, operator: ">=", value: 30 }],
			}),
		).toBe(`${field} >= 30.0`);
	});

	it("keeps count metrics as integer literals", () => {
		expect(
			alertQueryToCEL({
				combinator: "and",
				rules: [{ field: "provider_request_count", operator: ">=", value: 100 }],
			}),
		).toBe("provider_request_count >= 100");
	});

	it("keeps zero visible with the correct CEL numeric type", () => {
		expect(alertQueryToCEL({ combinator: "and", rules: [{ field: "provider_error_rate", operator: ">=", value: 0 }] })).toBe(
			"provider_error_rate >= 0.0",
		);
		expect(alertQueryToCEL({ combinator: "and", rules: [{ field: "provider_request_count", operator: ">=", value: 0 }] })).toBe(
			"provider_request_count >= 0",
		);
	});

	it("rejects fractional thresholds for integer metrics", () => {
		expect(
			buildAlertCEL({
				combinator: "and",
				rules: [{ field: "provider_request_count", operator: ">=", value: 100.5 }],
			}),
		).toEqual({ expression: "", error: "provider_request_count requires a whole-number threshold" });
	});

	it("does not silently turn malformed values into zero", () => {
		expect(
			buildAlertCEL({
				combinator: "and",
				rules: [{ field: "provider_error_rate", operator: ">=", value: "not-a-number" }],
			}),
		).toEqual({ expression: "", error: "provider_error_rate has an invalid numeric threshold" });
	});

	it("preserves integer thresholds beyond JavaScript safe-integer range", () => {
		expect(
			alertQueryToCEL({
				combinator: "and",
				rules: [{ field: "provider_request_count", operator: ">=", value: "9007199254740993" }],
			}),
		).toBe("provider_request_count >= 9007199254740993");
	});

	it("rejects integer thresholds outside the CEL int64 range", () => {
		expect(
			buildAlertCEL({
				combinator: "and",
				rules: [{ field: "provider_request_count", operator: ">=", value: "9223372036854775808" }],
			}),
		).toEqual({ expression: "", error: "provider_request_count is outside the supported integer range" });
	});

	it("formats nested groups with the correct literal types", () => {
		expect(
			alertQueryToCEL({
				combinator: "and",
				rules: [
					{ field: "provider_request_count", operator: ">=", value: 100 },
					{ combinator: "or", rules: [{ field: "provider_error_rate", operator: ">=", value: "30" }] },
				],
			}),
		).toBe("provider_request_count >= 100 && (provider_error_rate >= 30.0)");
	});
});