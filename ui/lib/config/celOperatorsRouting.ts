import { localize } from "@/lib/i18n/language";

/**
 * CEL Operators Configuration for Routing Rules
 * Maps UI operators to CEL syntax
 */

export interface CELOperatorDefinition {
	name: string;
	label: string;
	celSyntax: string;
}

export const celOperatorsRouting: CELOperatorDefinition[] = [
	// Comparison operators
	{ name: "=", label: localize("equals", "等于"), celSyntax: "==" },
	{ name: "!=", label: localize("does not equal", "不等于"), celSyntax: "!=" },
	{ name: ">", label: localize("greater than", "大于"), celSyntax: ">" },
	{ name: "<", label: localize("less than", "小于"), celSyntax: "<" },
	{ name: ">=", label: localize("greater than or equal", "大于或等于"), celSyntax: ">=" },
	{ name: "<=", label: localize("less than or equal", "小于或等于"), celSyntax: "<=" },

	// List operators
	{ name: "in", label: localize("is in list", "在列表中"), celSyntax: "in" },
	{ name: "notIn", label: localize("is not in list", "不在列表中"), celSyntax: "!in" },

	// String operators
	{ name: "contains", label: localize("contains", "包含"), celSyntax: "contains" },
	{ name: "beginsWith", label: localize("begins with", "开头为"), celSyntax: "startsWith" },
	{ name: "endsWith", label: localize("ends with", "结尾为"), celSyntax: "endsWith" },
	{ name: "matches", label: localize("matches (regex)", "匹配（正则）"), celSyntax: "matches" },

	// Existence operators
	{ name: "null", label: localize("does not exist", "不存在"), celSyntax: "!has" },
	{ name: "notNull", label: localize("exists", "存在"), celSyntax: "has" },
];

/**
 * Get CEL syntax for a given operator name
 */
export function getOperatorCELSyntax(operatorName: string): string {
	const operator = celOperatorsRouting.find((op) => op.name === operatorName);
	return operator ? operator.celSyntax : operatorName;
}

/**
 * Get operator label for display
 */
export function getOperatorLabel(operatorName: string): string {
	const operator = celOperatorsRouting.find((op) => op.name === operatorName);
	return operator ? operator.label : operatorName;
}
