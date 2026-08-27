import { RoutingErrorFallback, RoutingErrorFallbackFormData } from "@/lib/types/routingRules";

export function toErrorFallbackFormData(rule: RoutingErrorFallback): RoutingErrorFallbackFormData {
	const legacyCategories = rule.when?.categories || [];
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
			categories: legacyCategories,
			error_codes: rule.when?.error_codes || [],
			error_types: rule.when?.error_types || [],
			status_codes: rule.when?.status_codes || [],
			message_contains: rule.when?.message_contains || [],
		},
		fallbacks: rule.fallbacks || [],
	};
}

export function toErrorFallbackPayload(rule: RoutingErrorFallbackFormData): RoutingErrorFallback {
	if (rule.mode === "legacy" && rule.originalLegacyRule && legacyFormIsUntouched(rule, rule.originalLegacyRule)) {
		return structuredClone(rule.originalLegacyRule);
	}

	const base = {
		name: rule.name?.trim() || undefined,
		fallbacks: (rule.fallbacks || []).map(normalizeFallbackString).filter(hasFallbackProvider),
	};
	if (rule.mode === "legacy") {
		return {
			...base,
			when: {
				categories: rule.when.categories || [],
				error_codes: trimStrings(rule.when.error_codes),
				error_types: trimStrings(rule.when.error_types),
				status_codes: (rule.when.status_codes || []).filter(Number.isFinite),
				message_contains: trimStrings(rule.when.message_contains),
			},
		};
	}

	const supplement = {
		providers: trimStrings(rule.supplement.providers),
		error_codes: trimStrings(rule.supplement.error_codes),
		error_types: trimStrings(rule.supplement.error_types),
		status_codes: (rule.supplement.status_codes || []).filter(Number.isFinite),
		message_contains_any: trimStrings(rule.supplement.message_contains_any),
	};
	const hasSupplement = Object.values(supplement).some((values) => values.length > 0);
	return {
		...base,
		scenario: rule.scenario,
		supplement: hasSupplement ? supplement : undefined,
	};
}

export function switchErrorFallbackMode(
	rule: RoutingErrorFallbackFormData,
	mode: RoutingErrorFallbackFormData["mode"],
): RoutingErrorFallbackFormData {
	if (rule.mode === mode) return rule;
	if (mode === "scenario") return { ...rule, mode };

	const hasLegacyMatcher =
		rule.when.categories.length > 0 ||
		rule.when.error_codes.length > 0 ||
		rule.when.error_types.length > 0 ||
		rule.when.status_codes.length > 0 ||
		rule.when.message_contains.length > 0;
	return {
		...rule,
		mode,
		when: hasLegacyMatcher
			? rule.when
			: {
					...rule.when,
					categories: [rule.scenario],
				},
	};
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

function arraysEqual<T>(left: T[] = [], right: T[] = []): boolean {
	return left.length === right.length && left.every((value, index) => value === right[index]);
}

function trimStrings(values: string[] = []): string[] {
	return values.map((item) => item.trim()).filter(Boolean);
}

function normalizeFallbackString(fallback: string): string {
	const [provider = "", ...modelParts] = fallback.split("/");
	return `${provider.trim()}/${modelParts.join("/").trim()}`;
}

function hasFallbackProvider(fallback: string): boolean {
	return Boolean(fallback.split("/")[0]?.trim());
}