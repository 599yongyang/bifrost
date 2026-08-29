import i18n from "@/lib/i18n";

export function dashboardCopy() {
	return {
		routingRuleStats: i18n.t("workspace.dashboard.routingRuleStats"),
		routingRuleStatsDescription: i18n.t("workspace.dashboard.routingRuleStatsDescription"),
		routingRuleStatsError: i18n.t("workspace.dashboard.routingRuleStatsError"),
		noRoutingRuleStats: i18n.t("workspace.dashboard.noRoutingRuleStats"),
		routingRule: i18n.t("workspace.dashboard.routingRule"),
		requests: i18n.t("workspace.dashboard.requests"),
		successful: i18n.t("workspace.dashboard.successful"),
		failed: i18n.t("workspace.dashboard.failed"),
		requestVolume: i18n.t("workspace.dashboard.requestVolume"),
		total: i18n.t("workspace.dashboard.total"),
		success: i18n.t("workspace.logs.detail.success"),
		error: i18n.t("workspace.logs.detail.error"),
		cancelled: i18n.t("workspace.dashboard.cancelled"),
		tokenUsage: i18n.t("workspace.dashboard.tokenUsage"),
		input: i18n.t("workspace.dashboard.input"),
		output: i18n.t("workspace.dashboard.output"),
		cached: i18n.t("workspace.dashboard.cached"),
		externalCacheHitRate: i18n.t("workspace.dashboard.externalCacheHitRate"),
		localCacheHitRate: i18n.t("workspace.dashboard.localCacheHitRate"),
		cost: i18n.t("workspace.dashboard.cost"),
		modelUsage: i18n.t("workspace.dashboard.modelUsage"),
		latency: i18n.t("workspace.logs.colLatency"),
		average: i18n.t("workspace.dashboard.avg"),
		bifrostOverhead: i18n.t("workspace.dashboard.bifrostOverhead"),
		throughput: i18n.t("workspace.dashboard.throughput"),
		tokensPerSecond: i18n.t("workspace.dashboard.tokensPerSecond"),
		more: (count: number) => i18n.t("workspace.dashboard.moreCount", { count }),
	};
}