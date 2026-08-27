import { describe, expect, it } from "vitest";

import { RoutingErrorFallback } from "@/lib/types/routingRules";
import { switchErrorFallbackMode, toErrorFallbackFormData, toErrorFallbackPayload } from "@/lib/utils/errorFallbackRules";

describe("routing rule error fallback form compatibility", () => {
	it("keeps an untouched legacy when rule in legacy mode", () => {
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
		expect(form.when).toEqual({
			categories: ["content_policy"],
			error_codes: [],
			error_types: [],
			status_codes: [400],
			message_contains: ["unsafe"],
		});
		expect(form.supplement).toEqual({
			providers: [],
			error_codes: [],
			error_types: [],
			status_codes: [],
			message_contains_any: [],
		});
		expect(toErrorFallbackPayload(form)).toEqual(legacy);
		expect(toErrorFallbackPayload(form).when).not.toHaveProperty("error_codes");
	});

	it("normalizes a legacy rule only after the user edits it", () => {
		const legacy: RoutingErrorFallback = {
			when: { message_contains: ["unsafe"] },
			fallbacks: ["azure/gpt-image-1"],
		};
		const form = toErrorFallbackFormData(legacy);
		form.when.message_contains.push("内容被过滤");

		const payload = toErrorFallbackPayload(form);
		expect(payload.when?.message_contains).toEqual(["unsafe", "内容被过滤"]);
		expect(payload.when).toHaveProperty("error_codes", []);
	});

	it("opens scenario rules in the caller-first mode", () => {
		const scenario: RoutingErrorFallback = {
			scenario: "content_policy",
			supplement: {
				providers: ["custom-provider"],
				message_contains_any: ["请求被安全系统拒绝"],
			},
			fallbacks: ["xai/grok-image"],
		};

		const form = toErrorFallbackFormData(scenario);

		expect(form.mode).toBe("scenario");
		expect(form.scenario).toBe("content_policy");
		expect(form.supplement.providers).toEqual(["custom-provider"]);
		expect(form.supplement.message_contains_any).toEqual(["请求被安全系统拒绝"]);
	});

	it("can create a valid expert rule from a new scenario rule", () => {
		const scenario: RoutingErrorFallback = {
			scenario: "content_policy",
			fallbacks: ["azure/gpt-image-1"],
		};
		const expertForm = switchErrorFallbackMode(toErrorFallbackFormData(scenario), "legacy");
		const payload = toErrorFallbackPayload(expertForm);

		expect(expertForm.mode).toBe("legacy");
		expect(payload.scenario).toBeUndefined();
		expect(payload.when?.categories).toEqual(["content_policy"]);
	});
});