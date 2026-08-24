import type { AlertScopeType } from "@/lib/types/alerting";

export type AlertMetricDefinition = readonly [name: string, labelKey: string];

export const governanceAlertMetrics: AlertMetricDefinition[] = [
	["budget_usage_percent", "budgetUsedPercent"],
	["budget_spent", "budgetSpent"],
	["rate_limit_request_usage_percent", "requestLimitUsedPercent"],
	["rate_limit_token_usage_percent", "tokenLimitUsedPercent"],
	["request_usage", "requestUsage"],
	["token_usage", "tokenUsage"],
];

export const providerFailureAlertMetrics: AlertMetricDefinition[] = [
	["provider_error_rate", "providerErrorRate"],
	["provider_error_count", "providerErrorCount"],
	["provider_success_count", "providerSuccessCount"],
	["provider_request_count", "providerRequestCount"],
];

export function alertMetricsForScope(scopeType: AlertScopeType): AlertMetricDefinition[] {
	return scopeType === "provider" ? providerFailureAlertMetrics : governanceAlertMetrics;
}