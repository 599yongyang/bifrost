import type { RuleGroupType, RuleType } from "react-querybuilder";
import { governanceAlertMetrics, providerFailureAlertMetrics } from "./alertingRuleFields";

type Translate = (key: string) => string;

const metricLabelKeys = new Map([...governanceAlertMetrics, ...providerFailureAlertMetrics]);
const operatorSymbols: Record<string, string> = {
	">": ">",
	">=": "≥",
	"<": "<",
	"<=": "≤",
	"==": "=",
	"!=": "≠",
};
const countFields = new Set(["request_usage", "provider_error_count", "provider_success_count", "provider_request_count"]);
const percentFields = new Set([
	"budget_usage_percent",
	"rate_limit_request_usage_percent",
	"rate_limit_token_usage_percent",
	"provider_error_rate",
]);

function formatThreshold(field: string, value: unknown): string | null {
	if (value == null || String(value).trim() === "") return null;
	const rawValue = String(value).trim();
	const numericValue = Number(rawValue);
	const displayValue = Number.isFinite(numericValue) ? String(numericValue) : rawValue;
	if (percentFields.has(field)) return `${displayValue}%`;
	if (countFields.has(field)) return `${displayValue} 次`;
	if (field === "token_usage") return `${displayValue} Tokens`;
	return displayValue;
}

function formatGroup(group: RuleGroupType, translate: Translate, nested: boolean): string | null {
	const combinator = String(group.combinator).toLowerCase();
	if (combinator !== "and" && combinator !== "or") return null;
	const conditions: string[] = [];
	for (const entry of group.rules) {
		if ("rules" in entry) {
			const child = formatGroup(entry as RuleGroupType, translate, true);
			if (!child) return null;
			conditions.push(child);
			continue;
		}
		const rule = entry as RuleType;
		const field = String(rule.field || "");
		const labelKey = metricLabelKeys.get(field);
		const operator = operatorSymbols[String(rule.operator || "")];
		const threshold = formatThreshold(field, rule.value);
		if (!labelKey || !operator || !threshold) return null;
		conditions.push(`${translate(labelKey)} ${operator} ${threshold}`);
	}
	if (conditions.length === 0) return null;
	const connector = translate(combinator);
	const text = conditions.slice(1).reduce((result, condition) => {
		const separator = (connector === "且" || connector === "或") && condition.startsWith("（") ? ` ${connector}` : ` ${connector} `;
		return `${result}${separator}${condition}`;
	}, conditions[0]);
	return nested ? `（${text}）` : text;
}

export function formatAlertCondition(query: Record<string, unknown> | undefined, translate: Translate): string | null {
	if (!query || !Array.isArray(query.rules)) return null;
	return formatGroup(query as unknown as RuleGroupType, translate, false);
}