import { beforeAll, describe, expect, it } from "vitest";

import { setTestLanguage } from "@/lib/i18n/testUtils";
import type { RoutingErrorFallback } from "@/lib/types/routingRules";
import {
	mergeContentSafetyErrorFallbacks,
	switchErrorFallbackMode,
	toContentSafetyErrorFallbackFormData,
	toErrorFallbackFormData,
	toErrorFallbackPayload,
	validateErrorFallbackForms,
} from "@/lib/utils/errorFallbackRules";

beforeAll(async () => {
	await setTestLanguage("en");
});

describe("routing rule error fallback form compatibility", () => {
	it("keeps the stored content-safety matcher and supplements intact", () => {
		const forms = toContentSafetyErrorFallbackFormData([
			{ scenario: "timeout", fallbacks: ["azure/gpt-4o"] },
			{
				name: "provider safety",
				when: { categories: ["content_policy"], status_codes: [400] },
				supplement: { providers: ["custom"], message_contains_any: ["unsafe"] },
				fallbacks: ["xai/grok-image"],
			},
		]);

		expect(forms).toHaveLength(1);
		expect(toErrorFallbackPayload(forms[0])).toEqual({
			name: "provider safety",
			when: { categories: ["content_policy"], status_codes: [400] },
			supplement: { providers: ["custom"], message_contains_any: ["unsafe"] },
			fallbacks: ["xai/grok-image"],
		});
	});

	it("updates content-safety targets without deleting or reordering hidden rules", () => {
		const stored: RoutingErrorFallback[] = [
			{ scenario: "timeout", fallbacks: ["azure/gpt-4o"] },
			{
				scenario: "content_policy",
				supplement: { providers: ["custom"], error_codes: ["unsafe_prompt"] },
				fallbacks: ["xai/grok-image"],
			},
			{ scenario: "billing", fallbacks: ["openai/gpt-4o-mini"] },
		];
		const forms = toContentSafetyErrorFallbackFormData(stored);
		forms[0].fallbacks = ["google/gemini-2.5-flash"];

		expect(mergeContentSafetyErrorFallbacks(stored, forms)).toEqual([
			{ scenario: "timeout", fallbacks: ["azure/gpt-4o"] },
			{
				scenario: "content_policy",
				supplement: { providers: ["custom"], error_codes: ["unsafe_prompt"] },
				fallbacks: ["google/gemini-2.5-flash"],
			},
			{ scenario: "billing", fallbacks: ["openai/gpt-4o-mini"] },
		]);
	});

	it("disabling content-safety removes only content-safety rules", () => {
		const stored: RoutingErrorFallback[] = [
			{ scenario: "timeout", fallbacks: ["azure/gpt-4o"] },
			{ scenario: "content_policy", fallbacks: ["xai/grok-image"] },
			{ when: { categories: ["content_policy"] }, fallbacks: ["google/gemini"] },
			{ scenario: "billing", fallbacks: ["openai/gpt-4o-mini"] },
		];

		expect(mergeContentSafetyErrorFallbacks(stored, [])).toEqual([
			{ scenario: "timeout", fallbacks: ["azure/gpt-4o"] },
			{ scenario: "billing", fallbacks: ["openai/gpt-4o-mini"] },
		]);
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