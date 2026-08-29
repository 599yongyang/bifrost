import type { OtelFormSchema } from "@/lib/types/schemas";

type SelectiveExport = OtelFormSchema["selective_export"];
type SelectionRule = SelectiveExport["rules"][number];
type LegacySelectionRule = Partial<SelectionRule>;
type LegacySelectiveExport = Partial<Omit<SelectiveExport, "rules">> & { rules?: LegacySelectionRule[] };

const finiteNumber = (value: unknown): value is number => typeof value === "number" && Number.isFinite(value);
const bounded = (value: unknown, fallback: number, min: number, max: number) =>
	finiteNumber(value) ? Math.min(max, Math.max(min, value)) : fallback;

// IDs and priorities are implementation details in the policy-card UI. Repair
// legacy values here so an invisible validation error can never block saving.
export function normalizeSelectiveExportForForm(selection: LegacySelectiveExport | undefined, fallback: SelectiveExport): SelectiveExport {
	const source = selection ?? fallback;
	let rules = source.rules ?? fallback.rules;
	if (source.enabled && rules.length === 0) rules = fallback.rules;
	rules = [...rules].sort(
		(left, right) => (finiteNumber(right.priority) ? right.priority : 0) - (finiteNumber(left.priority) ? left.priority : 0),
	);

	const seen = new Set<string>();
	const normalizedRules = rules.map((rule, index): SelectionRule => {
		const requestedID = typeof rule.id === "string" && rule.id.trim() ? rule.id.trim() : `policy-${index + 1}`;
		let id = requestedID;
		let suffix = 2;
		while (seen.has(id)) id = `${requestedID}-${suffix++}`;
		seen.add(id);
		return {
			...rule,
			id,
			priority: (rules.length - index) * 10,
			request_types: rule.request_types ?? [],
			error_categories: rule.error_categories ?? [],
			providers: rule.providers ?? [],
			models: rule.models ?? [],
			routing_rules: rule.routing_rules ?? [],
			export_rate: bounded(rule.export_rate, 0.01, 0, 1),
			max_per_minute: Math.round(bounded(rule.max_per_minute, 0, 0, 10_000)),
		};
	});

	return {
		enabled: source.enabled ?? false,
		dry_run: source.dry_run ?? false,
		max_exports_per_minute: Math.round(bounded(source.max_exports_per_minute, 0, 0, 10_000)),
		rules: normalizedRules,
	};
}