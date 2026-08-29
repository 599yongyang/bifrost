import type { RoutingErrorFallback, RoutingErrorFallbackFormData } from "@/lib/types/routingRules";

const emptySupplement = () => ({ providers: [], error_codes: [], error_types: [], status_codes: [], message_contains_any: [] });
const emptyWhen = () => ({ categories: [], error_codes: [], error_types: [], status_codes: [], message_contains: [] });

// The routing-rule UI intentionally exposes one deep interface: a dedicated
// chain for content-policy failures. The backend retains the generalized schema
// for wire compatibility, but operators do not need to understand it.
export function toContentSafetyErrorFallbackFormData(rules: RoutingErrorFallback[]): RoutingErrorFallbackFormData[] {
	const configured = rules.find((rule) => rule.scenario === "content_policy" || rule.when?.categories?.includes("content_policy"));
	if (!configured) return [];
	const form = toErrorFallbackFormData(configured);
	return [
		{
			...form,
			// The compact UI edits only targets; the original wire contract remains
			// authoritative for every condition it does not expose.
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
		return { ...structuredClone(rule.originalContentSafetyRule), fallbacks };
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
				status_codes: rule.when.status_codes.filter(Number.isFinite),
				message_contains: trimStrings(rule.when.message_contains),
			},
		};
	}

	const supplement = {
		providers: trimStrings(rule.supplement.providers),
		error_codes: trimStrings(rule.supplement.error_codes),
		error_types: trimStrings(rule.supplement.error_types),
		status_codes: rule.supplement.status_codes.filter(Number.isFinite),
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
	let replaced = false;
	const merged: RoutingErrorFallback[] = [];

	for (const rule of existing) {
		if (!isContentSafetyRule(rule)) {
			merged.push(structuredClone(rule));
			continue;
		}
		if (!nextContentSafety) continue;
		if (!replaced) {
			merged.push(nextContentSafety);
			replaced = true;
		} else {
			merged.push(structuredClone(rule));
		}
	}

	if (nextContentSafety && !replaced) merged.push(nextContentSafety);
	return merged;
}

export function switchErrorFallbackMode(
	rule: RoutingErrorFallbackFormData,
	mode: RoutingErrorFallbackFormData["mode"],
): RoutingErrorFallbackFormData {
	if (rule.mode === mode) return rule;
	if (mode === "scenario") return { ...rule, mode };

	const hasLegacyMatcher = Object.values(rule.when).some((values) => values.length > 0);
	return {
		...rule,
		mode,
		when: hasLegacyMatcher ? rule.when : { ...rule.when, categories: [rule.scenario] },
	};
}

export function validateErrorFallbackForms(rules: RoutingErrorFallbackFormData[]): string[] {
	const errors: string[] = [];
	rules.forEach((rule, index) => {
		const label = `Error rule ${index + 1}`;
		if (rule.mode === "legacy" && Object.values(rule.when).every((values) => values.length === 0)) {
			errors.push(`${label} needs at least one matcher`);
		}
		const supplementSignals = [
			...rule.supplement.error_codes,
			...rule.supplement.error_types,
			...rule.supplement.status_codes,
			...rule.supplement.message_contains_any,
		];
		if (
			rule.mode === "scenario" &&
			!rule.originalContentSafetyRule &&
			rule.supplement.providers.length > 0 &&
			supplementSignals.length === 0
		) {
			errors.push(`${label} limits providers but has no supplemental recognition clue`);
		}

		const fallbacks = rule.fallbacks.map(normalizeFallbackString).filter(hasFallbackProvider);
		if (fallbacks.length === 0) errors.push(`${label} needs at least one fallback target`);
		if (new Set(fallbacks).size !== fallbacks.length) errors.push(`${label} contains duplicate fallback targets`);
	});
	return errors;
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

function arraysEqual<T>(left: T[] = [], right: T[] = []): boolean {
	return left.length === right.length && left.every((value, index) => value === right[index]);
}

function trimStrings(values: string[]): string[] {
	return values.map((item) => item.trim()).filter(Boolean);
}

function normalizeFallbackString(fallback: string): string {
	const [provider = "", ...modelParts] = fallback.split("/");
	return `${provider.trim()}/${modelParts.join("/").trim()}`;
}

function hasFallbackProvider(fallback: string): boolean {
	return Boolean(fallback.split("/")[0]?.trim());
}
