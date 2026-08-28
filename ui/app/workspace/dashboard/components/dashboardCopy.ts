const content = {
	en: {
		routingRuleStats: "Routing rule requests",
		routingRuleStatsDescription: "Request outcomes attributed to each routing rule in the selected period.",
		routingRuleStatsError: "Routing rule statistics could not be loaded.",
		noRoutingRuleStats: "No routed requests in this period.",
		routingRule: "Routing rule",
		requests: "Requests",
		successful: "Successful",
		failed: "Failed",
	},
	zh: {
		routingRuleStats: "路由规则请求统计",
		routingRuleStatsDescription: "所选时间范围内归属于各路由规则的请求结果。",
		routingRuleStatsError: "无法加载路由规则统计。",
		noRoutingRuleStats: "该时间范围内没有路由请求。",
		routingRule: "路由规则",
		requests: "请求数",
		successful: "成功",
		failed: "失败",
	},
} as const;

export function dashboardCopy() {
	return typeof document !== "undefined" && document.documentElement.lang.toLowerCase().startsWith("zh") ? content.zh : content.en;
}