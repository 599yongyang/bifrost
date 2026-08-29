import i18n from "@/lib/i18n";
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
	{ name: "=", label: i18n.t("workspace.routingRules.copy.celOperatorsRouting_equals"), celSyntax: "==" },
	{ name: "!=", label: i18n.t("workspace.routingRules.copy.celOperatorsRouting_does_not_equal"), celSyntax: "!=" },
	{ name: ">", label: i18n.t("workspace.routingRules.copy.celOperatorsRouting_greater_than"), celSyntax: ">" },
	{ name: "<", label: i18n.t("workspace.routingRules.copy.celOperatorsRouting_less_than"), celSyntax: "<" },
	{ name: ">=", label: i18n.t("workspace.routingRules.copy.celOperatorsRouting_greater_than_or_equal"), celSyntax: ">=" },
	{ name: "<=", label: i18n.t("workspace.routingRules.copy.celOperatorsRouting_less_than_or_equal"), celSyntax: "<=" },

	// List operators
	{ name: "in", label: i18n.t("workspace.routingRules.copy.celOperatorsRouting_is_in_list"), celSyntax: "in" },
	{ name: "notIn", label: i18n.t("workspace.routingRules.copy.celOperatorsRouting_is_not_in_list"), celSyntax: "!in" },

	// String operators
	{ name: "contains", label: i18n.t("workspace.routingRules.copy.celOperatorsRouting_contains"), celSyntax: "contains" },
	{ name: "beginsWith", label: i18n.t("workspace.routingRules.copy.celOperatorsRouting_begins_with"), celSyntax: "startsWith" },
	{ name: "endsWith", label: i18n.t("workspace.routingRules.copy.celOperatorsRouting_ends_with"), celSyntax: "endsWith" },
	{ name: "matches", label: i18n.t("workspace.routingRules.copy.celOperatorsRouting_matches_regex"), celSyntax: "matches" },

	// Existence operators
	{ name: "null", label: i18n.t("workspace.routingRules.copy.celOperatorsRouting_does_not_exist"), celSyntax: "!has" },
	{ name: "notNull", label: i18n.t("workspace.routingRules.copy.celOperatorsRouting_exists"), celSyntax: "has" },
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