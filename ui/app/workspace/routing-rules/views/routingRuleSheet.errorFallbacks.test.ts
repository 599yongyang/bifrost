import { describe, expect, it } from "vitest";

import type { RoutingErrorFallback } from "@/lib/types/routingRules";
import {
	switchErrorFallbackMode,
	toContentSafetyErrorFallbackFormData,
	toErrorFallbackFormData,
	toErrorFallbackPayload,
	validateErrorFallbackForms,
} from "@/lib/utils/errorFallbackRules";

describe("routing rule error fallback form compatibility", () => {
	it("reduces stored rules to one content-safety exception", () => {
		const forms = toContentSafetyErrorFallbackFormData([
			{ scenario: "timeout", fallbacks: ["azure/gpt-4o"] },
			{ when: { categories: ["content_policy"], status_codes: [400] }, fallbacks: ["xai/grok-image"] },
		]);

		expect(forms).toHaveLength(1);
		expect(toErrorFallbackPayload(forms[0])).toEqual({
			name: "content-safety",
			scenario: "content_policy",
			fallbacks: ["xai/grok-image"],
		});
	});
	it("keeps an untouched legacy when rule unchanged", () => {
		const legacy: RoutingErrorFallback = {
			name: "legacy safety rule",
			when: {
				categories: ["content_policy"],
				status_codes: [400],
				message_contains: ["unsafe"],
			},
			fallbacks: ["azure/gpt-image-1"],
		};

		const form = toErrorFallbackFormData(legacy);
		expect(form.mode).toBe("legacy");
		expect(toErrorFallbackPayload(form)).toEqual(legacy);
		expect(toErrorFallbackPayload(form).when).not.toHaveProperty("error_codes");
	});

	it("converts scenario rules and their supplemental clues", () => {
		const form = toErrorFallbackFormData({
			scenario: "content_policy",
			supplement: {
				providers: ["custom-provider"],
				message_contains_any: ["请求被安全系统拒绝"],
			},
			fallbacks: ["xai/grok-image"],
		});

		expect(form.mode).toBe("scenario");
		expect(form.supplement.providers).toEqual(["custom-provider"]);
		expect(toErrorFallbackPayload(form)).toEqual({
			scenario: "content_policy",
			supplement: {
				providers: ["custom-provider"],
				error_codes: [],
				error_types: [],
				status_codes: [],
				message_contains_any: ["请求被安全系统拒绝"],
			},
			fallbacks: ["xai/grok-image"],
		});
	});

	it("switches a new scenario rule to an equivalent expert matcher", () => {
		const expert = switchErrorFallbackMode(toErrorFallbackFormData({ scenario: "timeout", fallbacks: ["azure/gpt-4o"] }), "legacy");

		expect(toErrorFallbackPayload(expert).when?.categories).toEqual(["timeout"]);
	});

	it("rejects matcherless, empty and duplicate fallback forms", () => {
		const matcherless = switchErrorFallbackMode(toErrorFallbackFormData({ scenario: "timeout", fallbacks: ["azure/gpt-4o"] }), "legacy");
		matcherless.when.categories = [];
		expect(validateErrorFallbackForms([matcherless])).toContain("Error rule 1 needs at least one matcher");

		const empty = toErrorFallbackFormData({ scenario: "timeout", fallbacks: [] });
		expect(validateErrorFallbackForms([empty])).toContain("Error rule 1 needs at least one fallback target");

		const duplicate = toErrorFallbackFormData({
			scenario: "timeout",
			fallbacks: ["azure/gpt-4o", "azure/gpt-4o"],
		});
		expect(validateErrorFallbackForms([duplicate])).toContain("Error rule 1 contains duplicate fallback targets");
	});
});