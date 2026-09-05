import type { RoutingErrorFallback, RoutingErrorFallbackFormData } from "@/lib/types/routingRules";
import i18n from "@/lib/i18n";

// The routing-rule UI exposes content-safety recognition plus an optional
// dedicated chain. The backend retains the generalized schema for wire
// compatibility, but operators do not need to understand it.
export function toContentSafetyErrorFallbackFormData(rules: RoutingErrorFallback[]): RoutingErrorFallbackFormData[] {
	const configured = rules.find((rule) => rule.scenario === "content_policy" || rule.when?.categories?.includes("content_policy"));
	if (!configured) return [];
	const form = toErrorFallbackFormData(configured);
	if (!configured.scenario) {
		form.supplement = legacyRecognitionAsSupplement(configured);
	}
	return [
		{
			...form,
			// The compact UI edits targets and visible recognition clues; the original
			// wire contract remains authoritative for every hidden condition.
			mode: "scenario",
			scenario: "content_policy",
			originalContentSafetyRule: structuredClone(configured),
		},
	];
}

export function toErrorFallbackFormData(rule: RoutingErrorFallback): RoutingErrorFallbackFormData {
	const usesScenario = Boolean(rule.scenario);
	return {
		mode: usesScenario ? "scenario" : "legacy",
		originalLegacyRule: usesScenario ? undefined : structuredClone(rule),
		name: rule.name || "",
		scenario: rule.scenario || "content_policy",
		supplement: {
			providers: rule.supplement?.providers || [],
			error_codes: rule.supplement?.error_codes || [],
			error_types: rule.supplement?.error_types || [],
			status_codes: rule.supplement?.status_codes || [],
			message_contains_any: rule.supplement?.message_contains_any || [],
		},
		when: {
			categories: rule.when?.categories || [],
			error_codes: rule.when?.error_codes || [],
			error_types: rule.when?.error_types || [],
			status_codes: rule.when?.status_codes || [],
			message_contains: rule.when?.message_contains || [],
		},
		fallbacks: rule.fallbacks || [],
	};
}

export function toErrorFallbackPayload(rule: RoutingErrorFallbackFormData): RoutingErrorFallback {
	const fallbacks = rule.fallbacks.map(normalizeFallbackString).filter(hasFallbackProvider);
	if (rule.originalContentSafetyRule) {
		const original = structuredClone(rule.originalContentSafetyRule);
		if (!original.scenario) return legacyContentSafetyPayload(rule, original, fallbacks);
		if (contentSafetyRecognitionIsUntouched(rule, original)) return { ...original, fallbacks };
		return scenarioContentSafetyPayload(rule, fallbacks);
	}
	if (rule.mode === "legacy" && rule.originalLegacyRule && legacyFormIsUntouched(rule, rule.originalLegacyRule)) {
		return structuredClone(rule.originalLegacyRule);
	}

	const base = {
		name: rule.name?.trim() || undefined,
		fallbacks,
	};
	if (rule.mode === "legacy") {
		return {
			...base,
			when: {
				categories: rule.when.categories || [],
				error_codes: trimStrings(rule.when.error_codes),
				error_types: trimStrings(rule.when.error_types),
				status_codes: rule.when.status_codes.filter(isValidHttpStatusCode),
				message_contains: trimStrings(rule.when.message_contains),
			},
		};
	}

	const supplement = {
		providers: trimStrings(rule.supplement.providers),
		error_codes: trimStrings(rule.supplement.error_codes),
		error_types: trimStrings(rule.supplement.error_types),
		status_codes: rule.supplement.status_codes.filter(isValidHttpStatusCode),
		message_contains_any: trimStrings(rule.supplement.message_contains_any),
	};
	const hasSupplement = Object.values(supplement).some((values) => values.length > 0);
	return {
		...base,
		scenario: rule.scenario,
		supplement: hasSupplement ? supplement : undefined,
	};
}

// The compact UI owns only the first content-safety rule. Every hidden rule is
// an opaque compatibility contract and must retain its original position.
export function mergeContentSafetyErrorFallbacks(
	existing: RoutingErrorFallback[],
	forms: RoutingErrorFallbackFormData[],
): RoutingErrorFallback[] {
	const nextContentSafety = forms[0] ? toErrorFallbackPayload(forms[0]) : undefined;
	const fallbackEnabled = Boolean(nextContentSafety?.fallbacks.length);
	let replaced = false;
	const merged: RoutingErrorFallback[] = [];

	for (const rule of existing) {
		if (!isContentSafetyRule(rule)) {
			merged.push(structuredClone(rule));
			continue;
		}
		if (!nextContentSafety) {
			const recognitionOnly = toRecognitionOnlyContentSafetyRule(rule);
			if (recognitionOnly) merged.push(recognitionOnly);
			continue;
		}
		if (!replaced) {
			if (fallbackEnabled || hasPersistedRecognitionClues(nextContentSafety)) merged.push(nextContentSafety);
			replaced = true;
		} else if (fallbackEnabled) {
			merged.push(structuredClone(rule));
		} else {
			const recognitionOnly = toRecognitionOnlyContentSafetyRule(rule);
			if (recognitionOnly) merged.push(recognitionOnly);
		}
	}

	if (nextContentSafety && !replaced && (fallbackEnabled || hasPersistedRecognitionClues(nextContentSafety))) {
		merged.push(nextContentSafety);
	}
	return merged;
}

export function hasCustomContentSafetyRecognition(rule: RoutingErrorFallbackFormData): boolean {
	return (
		hasNormalizedStrings(rule.supplement.error_codes) ||
		hasNormalizedStrings(rule.supplement.error_types) ||
		rule.supplement.status_codes.some(isValidHttpStatusCode) ||
		hasNormalizedStrings(rule.supplement.message_contains_any) ||
		hasNormalizedStrings(rule.when.error_codes) ||
		hasNormalizedStrings(rule.when.error_types) ||
		rule.when.status_codes.some(isValidHttpStatusCode) ||
		hasNormalizedStrings(rule.when.message_contains)
	);
}

export function hasContentSafetyRecognitionDraft(rule: RoutingErrorFallbackFormData): boolean {
	return hasNormalizedStrings(rule.supplement.providers) || hasCustomContentSafetyRecognition(rule);
}

function hasPersistedRecognitionClues(rule: RoutingErrorFallback): boolean {
	return (
		hasNormalizedStrings(rule.supplement?.error_codes || []) ||
		hasNormalizedStrings(rule.supplement?.error_types || []) ||
		Boolean(rule.supplement?.status_codes?.some(isValidHttpStatusCode)) ||
		hasNormalizedStrings(rule.supplement?.message_contains_any || []) ||
		hasNormalizedStrings(rule.when?.error_codes || []) ||
		hasNormalizedStrings(rule.when?.error_types || []) ||
		Boolean(rule.when?.status_codes?.some(isValidHttpStatusCode)) ||
		hasNormalizedStrings(rule.when?.message_contains || [])
	);
}

function toRecognitionOnlyContentSafetyRule(rule: RoutingErrorFallback): RoutingErrorFallback | undefined {
	if (!hasPersistedRecognitionClues(rule)) return undefined;
	return { ...structuredClone(rule), fallbacks: [] };
}

export function switchErrorFallbackMode(
	rule: RoutingErrorFallbackFormData,
	mode: RoutingErrorFallbackFormData["mode"],
): RoutingErrorFallbackFormData {
	if (rule.mode === mode) return rule;
	if (mode === "scenario") return { ...rule, mode };

	const hasLegacyMatcher = hasConditionMatchers(rule.when);
	return {
		...rule,
		mode,
		when: hasLegacyMatcher ? rule.when : { ...rule.when, categories: [rule.scenario] },
	};
}

export function validateErrorFallbackForms(rules: RoutingErrorFallbackFormData[]): string[] {
	const errors: string[] = [];
	rules.forEach((rule, index) => {
		const label = i18n.t("workspace.routingRules.copy.errorFallbackRules_error_rule", { value0: index + 1 });
		if (rule.mode === "legacy" && !hasConditionMatchers(rule.when)) {
			errors.push(i18n.t("workspace.routingRules.copy.errorFallbackRules_needs_at_least_one_matcher", { value0: label }));
		}
		const hasSupplementSignals =
			hasNormalizedStrings(rule.supplement.error_codes) ||
			hasNormalizedStrings(rule.supplement.error_types) ||
			rule.supplement.status_codes.some(isValidHttpStatusCode) ||
			hasNormalizedStrings(rule.supplement.message_contains_any);
		if (rule.mode === "scenario" && hasNormalizedStrings(rule.supplement.providers) && !hasSupplementSignals) {
			errors.push(
				i18n.t("workspace.routingRules.copy.errorFallbackRules_limits_providers_but_has_no_supplemental_recognition_clu", {
					value0: label,
				}),
			);
		}
		if (rule.originalContentSafetyRule?.supplement && hasConditionMatchers(rule.originalContentSafetyRule.when)) {
			errors.push(i18n.t("workspace.routingRules.copy.errorFallbackRules_mixed_legacy_contract"));
		}
		const invalidStatusCodes = [...rule.supplement.status_codes, ...rule.when.status_codes].filter(
			(value) => !isValidHttpStatusCode(value),
		);
		if (invalidStatusCodes.length > 0) {
			errors.push(
				i18n.t("workspace.routingRules.copy.errorFallbackRules_invalid_status_codes", {
					value0: label,
					value1: invalidStatusCodes.join(", "),
				}),
			);
		}

		const fallbacks = rule.fallbacks.map(normalizeFallbackString).filter(hasFallbackProvider);
		const recognitionOnlyContentSafety =
			isContentSafetyFormRule(rule) && rule.fallbacks.length === 0 && hasCustomContentSafetyRecognition(rule);
		const clearingStoredContentSafety =
			isContentSafetyFormRule(rule) && rule.fallbacks.length === 0 && Boolean(rule.originalContentSafetyRule);
		if (fallbacks.length === 0 && !recognitionOnlyContentSafety && !clearingStoredContentSafety)
			errors.push(i18n.t("workspace.routingRules.copy.errorFallbackRules_needs_at_least_one_fallback_target", { value0: label }));
		if (new Set(fallbacks).size !== fallbacks.length)
			errors.push(i18n.t("workspace.routingRules.copy.errorFallbackRules_contains_duplicate_fallback_targets", { value0: label }));
	});
	return errors;
}

function isContentSafetyFormRule(rule: RoutingErrorFallbackFormData): boolean {
	return rule.scenario === "content_policy" || rule.when.categories.includes("content_policy");
}

function isContentSafetyRule(rule: RoutingErrorFallback): boolean {
	return rule.scenario === "content_policy" || Boolean(rule.when?.categories?.includes("content_policy"));
}

function legacyFormIsUntouched(form: RoutingErrorFallbackFormData, original: RoutingErrorFallback): boolean {
	const when = original.when || {};
	return (
		form.name === (original.name || "") &&
		arraysEqual(form.fallbacks, original.fallbacks || []) &&
		arraysEqual(form.when.categories, when.categories || []) &&
		arraysEqual(form.when.error_codes, when.error_codes || []) &&
		arraysEqual(form.when.error_types, when.error_types || []) &&
		arraysEqual(form.when.status_codes, when.status_codes || []) &&
		arraysEqual(form.when.message_contains, when.message_contains || [])
	);
}

function legacyRecognitionAsSupplement(rule: RoutingErrorFallback): RoutingErrorFallbackFormData["supplement"] {
	return {
		providers: [...(rule.supplement?.providers || [])],
		error_codes: [],
		error_types: [],
		status_codes: [],
		message_contains_any: [...(rule.when?.message_contains || [])],
	};
}

function contentSafetyRecognitionIsUntouched(form: RoutingErrorFallbackFormData, original: RoutingErrorFallback): boolean {
	const originalSupplement = original.scenario
		? {
				providers: original.supplement?.providers || [],
				error_codes: original.supplement?.error_codes || [],
				error_types: original.supplement?.error_types || [],
				status_codes: original.supplement?.status_codes || [],
				message_contains_any: original.supplement?.message_contains_any || [],
			}
		: legacyRecognitionAsSupplement(original);
	return (
		arraysEqual(trimStrings(form.supplement.providers), trimStrings(originalSupplement.providers)) &&
		arraysEqual(trimStrings(form.supplement.error_codes), trimStrings(originalSupplement.error_codes)) &&
		arraysEqual(trimStrings(form.supplement.error_types), trimStrings(originalSupplement.error_types)) &&
		arraysEqual(form.supplement.status_codes, originalSupplement.status_codes) &&
		arraysEqual(trimStrings(form.supplement.message_contains_any), trimStrings(originalSupplement.message_contains_any))
	);
}

function scenarioContentSafetyPayload(rule: RoutingErrorFallbackFormData, fallbacks: string[]): RoutingErrorFallback {
	const supplement = {
		providers: trimStrings(rule.supplement.providers),
		error_codes: trimStrings(rule.supplement.error_codes),
		error_types: trimStrings(rule.supplement.error_types),
		status_codes: rule.supplement.status_codes.filter(isValidHttpStatusCode),
		message_contains_any: trimStrings(rule.supplement.message_contains_any),
	};
	const compactSupplement = Object.fromEntries(Object.entries(supplement).filter(([, values]) => values.length > 0));
	return {
		name: rule.name.trim() || undefined,
		scenario: "content_policy",
		supplement: Object.keys(compactSupplement).length > 0 ? compactSupplement : undefined,
		fallbacks,
	};
}

function legacyContentSafetyPayload(
	rule: RoutingErrorFallbackFormData,
	original: RoutingErrorFallback,
	fallbacks: string[],
): RoutingErrorFallback {
	const when = structuredClone(original.when || {});
	const messages = trimStrings(rule.supplement.message_contains_any);
	if (messages.length > 0) when.message_contains = messages;
	else delete when.message_contains;
	return { ...original, fallbacks, when };
}

function hasNormalizedStrings(values: string[]): boolean {
	return trimStrings(values).length > 0;
}

function hasConditionMatchers(condition: RoutingErrorFallback["when"]): boolean {
	if (!condition) return false;
	return (
		hasNormalizedStrings(condition.categories || []) ||
		hasNormalizedStrings(condition.error_codes || []) ||
		hasNormalizedStrings(condition.error_types || []) ||
		Boolean(condition.status_codes?.some(isValidHttpStatusCode)) ||
		hasNormalizedStrings(condition.message_contains || [])
	);
}

function arraysEqual<T>(left: T[] = [], right: T[] = []): boolean {
	return left.length === right.length && left.every((value, index) => value === right[index]);
}

function trimStrings(values: string[]): string[] {
	return values.map((item) => item.trim()).filter(Boolean);
}

function isValidHttpStatusCode(value: number): boolean {
	return Number.isInteger(value) && value >= 100 && value <= 599;
}

function normalizeFallbackString(fallback: string): string {
	const [provider = "", ...modelParts] = fallback.split("/");
	return `${provider.trim()}/${modelParts.join("/").trim()}`;
}

function hasFallbackProvider(fallback: string): boolean {
	return Boolean(fallback.split("/")[0]?.trim());
}