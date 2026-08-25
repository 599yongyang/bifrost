import type { RuleGroupType, RuleType } from "react-querybuilder";

export type AlertMetricNumericKind = "double" | "int";

const metricNumericKinds: Record<string, AlertMetricNumericKind> = {
	budget_usage_percent: "double",
	budget_spent: "double",
	budget_limit: "double",
	rate_limit_request_usage_percent: "double",
	rate_limit_token_usage_percent: "double",
	provider_error_rate: "double",
	request_usage: "int",
	request_limit: "int",
	token_usage: "int",
	token_limit: "int",
	provider_error_count: "int",
	provider_success_count: "int",
	provider_request_count: "int",
	window_seconds: "int",
};

const minCELInt = BigInt("-9223372036854775808");
const maxCELInt = BigInt("9223372036854775807");

export type AlertCELBuildResult = {
	expression: string;
	error: string | null;
};

export function alertMetricNumericKind(field: string): AlertMetricNumericKind | undefined {
	return metricNumericKinds[field];
}

function celNumericLiteral(field: string, value: unknown): { literal: string; error: string | null } {
	const kind = alertMetricNumericKind(field);
	if (!kind) return { literal: "", error: `${field} is not a supported alert metric` };
	const rawValue = String(value).trim();
	if (kind === "int") {
		if (!/^-?\d+$/.test(rawValue)) {
			return { literal: "", error: `${field} requires a whole-number threshold` };
		}
		try {
			const integerValue = BigInt(rawValue);
			if (integerValue < minCELInt || integerValue > maxCELInt) {
				return { literal: "", error: `${field} is outside the supported integer range` };
			}
			return { literal: integerValue.toString(), error: null };
		} catch {
			return { literal: "", error: `${field} has an invalid integer threshold` };
		}
	}
	if (!/^-?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/.test(rawValue)) {
		return { literal: "", error: `${field} has an invalid numeric threshold` };
	}
	const numericValue = Number(rawValue);
	if (!Number.isFinite(numericValue)) {
		return { literal: "", error: `${field} has an invalid numeric threshold` };
	}
	return {
		literal: Number.isInteger(numericValue) ? `${numericValue}.0` : String(numericValue),
		error: null,
	};
}

export function buildAlertCEL(group: RuleGroupType | undefined): AlertCELBuildResult {
	if (!group?.rules?.length) return { expression: "", error: null };
	const expressions: string[] = [];
	for (const entry of group.rules) {
		if ("rules" in entry) {
			const nested = buildAlertCEL(entry as RuleGroupType);
			if (nested.error) return nested;
			if (nested.expression) expressions.push(`(${nested.expression})`);
			continue;
		}
		const rule = entry as RuleType;
		if (!rule.field || !rule.operator || rule.value === "" || rule.value == null) continue;
		const field = String(rule.field);
		const literal = celNumericLiteral(field, rule.value);
		if (literal.error) return { expression: "", error: literal.error };
		expressions.push(`${field} ${rule.operator} ${literal.literal}`);
	}
	return {
		expression: expressions.join(String(group.combinator).toLowerCase() === "or" ? " || " : " && "),
		error: null,
	};
}

export function alertQueryToCEL(group: RuleGroupType | undefined): string {
	return buildAlertCEL(group).expression;
}