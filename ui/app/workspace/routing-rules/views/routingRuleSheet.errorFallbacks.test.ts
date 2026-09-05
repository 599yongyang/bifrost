import { beforeAll, describe, expect, it } from "vitest";

import { setTestLanguage } from "@/lib/i18n/testUtils";
import type { RoutingErrorFallback } from "@/lib/types/routingRules";
import {
	hasContentSafetyRecognitionDraft,
	hasCustomContentSafetyRecognition,
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
	it("rejects a mixed legacy content-safety contract instead of silently changing its semantics", () => {
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
		expect(validateErrorFallbackForms(forms)).toContain(
			"This legacy content-safety rule combines unsupported matcher formats. Recreate it before saving.",
		);
	});

	it("accepts the empty when object emitted by the Go API for scenario rules", () => {
		const forms = toContentSafetyErrorFallbackFormData([
			{
				scenario: "content_policy",
				supplement: {
					providers: ["custom-provider"],
					message_contains_any: ["vendor moderation gate"],
				},
				when: {},
				fallbacks: [],
			},
		]);

		expect(validateErrorFallbackForms(forms)).toEqual([]);
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

	it("updates configurable message clues while preserving hidden supplemental matchers", () => {
		const stored: RoutingErrorFallback[] = [
			{
				scenario: "content_policy",
				supplement: {
					providers: ["custom"],
					error_codes: ["unsafe_prompt"],
					status_codes: [400],
					message_contains_any: ["old wording"],
				},
				fallbacks: ["xai/grok-image"],
			},
		];
		const forms = toContentSafetyErrorFallbackFormData(stored);
		forms[0].supplement.providers = ["custom", "second-provider"];
		forms[0].supplement.message_contains_any = ["内容政策", "safety system"];

		expect(toErrorFallbackPayload(forms[0])).toEqual({
			scenario: "content_policy",
			supplement: {
				providers: ["custom", "second-provider"],
				error_codes: ["unsafe_prompt"],
				status_codes: [400],
				message_contains_any: ["内容政策", "safety system"],
			},
			fallbacks: ["xai/grok-image"],
		});
	});

	it("disabling content-safety removes only content-safety rules", () => {
		const stored: RoutingErrorFallback[] = [
			{ scenario: "timeout", fallbacks: ["azure/gpt-4o"] },
			{ scenario: "content_policy", fallbacks: ["xai/grok-image"] },
			{
				when: { categories: ["content_policy"] },
				fallbacks: ["google/gemini"],
			},
			{ scenario: "billing", fallbacks: ["openai/gpt-4o-mini"] },
		];

		expect(mergeContentSafetyErrorFallbacks(stored, [])).toEqual([
			{ scenario: "timeout", fallbacks: ["azure/gpt-4o"] },
			{ scenario: "billing", fallbacks: ["openai/gpt-4o-mini"] },
		]);
	});

	it("keeps custom recognition clues when content-safety fallback is disabled", () => {
		const stored: RoutingErrorFallback[] = [
			{ scenario: "timeout", fallbacks: ["azure/gpt-4o"] },
			{
				name: "custom safety wording",
				scenario: "content_policy",
				supplement: {
					providers: ["custom"],
					message_contains_any: ["vendor moderation gate"],
				},
				fallbacks: ["xai/grok-image"],
			},
		];
		const forms = toContentSafetyErrorFallbackFormData(stored);
		forms[0].fallbacks = [];

		expect(validateErrorFallbackForms(forms)).toEqual([]);
		expect(mergeContentSafetyErrorFallbacks(stored, forms)).toEqual([
			{ scenario: "timeout", fallbacks: ["azure/gpt-4o"] },
			{
				name: "custom safety wording",
				scenario: "content_policy",
				supplement: {
					providers: ["custom"],
					message_contains_any: ["vendor moderation gate"],
				},
				fallbacks: [],
			},
		]);
	});

	it("keeps legacy content-safety recognition when its fallback is disabled", () => {
		const stored: RoutingErrorFallback[] = [
			{
				name: "legacy safety wording",
				when: { categories: ["content_policy"], message_contains: ["legacy moderation gate"] },
				fallbacks: ["azure/gpt-image-1"],
			},
		];
		const forms = toContentSafetyErrorFallbackFormData(stored);
		forms[0].fallbacks = [];

		expect(validateErrorFallbackForms(forms)).toEqual([]);
		expect(mergeContentSafetyErrorFallbacks(stored, forms)).toEqual([
			{
				name: "legacy safety wording",
				when: { categories: ["content_policy"], message_contains: ["legacy moderation gate"] },
				fallbacks: [],
			},
		]);
	});

	it("removes the last stored custom clue while fallback remains disabled", () => {
		const stored: RoutingErrorFallback[] = [
			{
				scenario: "content_policy",
				supplement: { message_contains_any: ["obsolete moderation wording"] },
				fallbacks: [],
			},
		];
		const forms = toContentSafetyErrorFallbackFormData(stored);
		forms[0].supplement.message_contains_any = [];

		expect(validateErrorFallbackForms(forms)).toEqual([]);
		expect(mergeContentSafetyErrorFallbacks(stored, forms)).toEqual([]);
	});

	it("keeps a provider-first custom recognition draft without treating it as saveable recognition", () => {
		const form = toErrorFallbackFormData({ scenario: "content_policy", fallbacks: [] });
		form.supplement.providers = ["custom-provider"];

		expect(hasContentSafetyRecognitionDraft(form)).toBe(true);
		expect(hasCustomContentSafetyRecognition(form)).toBe(false);
		expect(validateErrorFallbackForms([form])).toContain("Error rule 1 limits providers but has no supplemental recognition clue");
	});

	it("rejects a provider-only edit of a stored content-safety rule", () => {
		const [form] = toContentSafetyErrorFallbackFormData([
			{
				scenario: "content_policy",
				supplement: { providers: ["custom-provider"], message_contains_any: ["unsafe"] },
				fallbacks: [],
			},
		]);
		form.supplement.message_contains_any = [];

		expect(validateErrorFallbackForms([form])).toContain("Error rule 1 limits providers but has no supplemental recognition clue");
	});

	it("edits a legacy content-safety matcher without changing its AND semantics", () => {
		const [form] = toContentSafetyErrorFallbackFormData([
			{
				name: "legacy safety wording",
				when: {
					categories: ["content_policy"],
					status_codes: [400],
					message_contains: ["legacy moderation gate"],
				},
				fallbacks: ["azure/gpt-image-1"],
			},
		]);
		form.supplement.message_contains_any = ["legacy moderation gate", "new moderation gate"];

		expect(toErrorFallbackPayload(form)).toEqual({
			name: "legacy safety wording",
			when: {
				categories: ["content_policy"],
				status_codes: [400],
				message_contains: ["legacy moderation gate", "new moderation gate"],
			},
			fallbacks: ["azure/gpt-image-1"],
		});
	});

	it("does not treat whitespace-only recognition text as a saveable clue", () => {
		const form = toErrorFallbackFormData({ scenario: "content_policy", fallbacks: [] });
		form.supplement.message_contains_any = ["   "];

		expect(hasCustomContentSafetyRecognition(form)).toBe(false);
		expect(validateErrorFallbackForms([form])).toContain("Error rule 1 needs at least one fallback target");
	});

	it("rejects whitespace-only matchers in legacy rules", () => {
		const legacy = toErrorFallbackFormData({
			when: {
				error_codes: ["   "],
				error_types: ["\t"],
				message_contains: ["\n"],
			},
			fallbacks: ["openai/gpt-4o"],
		});

		expect(validateErrorFallbackForms([legacy])).toContain("Error rule 1 needs at least one matcher");
	});

	it("uses the API and schema HTTP status range for custom recognition", () => {
		const invalid = toErrorFallbackFormData({ scenario: "content_policy", fallbacks: [] });
		invalid.supplement.status_codes = [99, 600, 200.5];

		expect(hasCustomContentSafetyRecognition(invalid)).toBe(false);
		expect(validateErrorFallbackForms([invalid])).toContain(
			"Error rule 1 contains invalid HTTP status codes: 99, 600, 200.5. Use integers from 100 to 599.",
		);

		const valid = toErrorFallbackFormData({ scenario: "content_policy", fallbacks: [] });
		valid.supplement.status_codes = [100, 599];
		expect(hasCustomContentSafetyRecognition(valid)).toBe(true);
		expect(validateErrorFallbackForms([valid])).toEqual([]);
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
		const expert = switchErrorFallbackMode(
			toErrorFallbackFormData({
				scenario: "timeout",
				fallbacks: ["azure/gpt-4o"],
			}),
			"legacy",
		);

		expect(toErrorFallbackPayload(expert).when?.categories).toEqual(["timeout"]);
	});

	it("rejects matcherless, empty and duplicate fallback forms", () => {
		const matcherless = switchErrorFallbackMode(
			toErrorFallbackFormData({
				scenario: "timeout",
				fallbacks: ["azure/gpt-4o"],
			}),
			"legacy",
		);
		matcherless.when.categories = [];
		expect(validateErrorFallbackForms([matcherless])).toContain("Error rule 1 needs at least one matcher");

		const empty = toErrorFallbackFormData({
			scenario: "timeout",
			fallbacks: [],
		});
		expect(validateErrorFallbackForms([empty])).toContain("Error rule 1 needs at least one fallback target");

		const duplicate = toErrorFallbackFormData({
			scenario: "timeout",
			fallbacks: ["azure/gpt-4o", "azure/gpt-4o"],
		});
		expect(validateErrorFallbackForms([duplicate])).toContain("Error rule 1 contains duplicate fallback targets");
	});
});