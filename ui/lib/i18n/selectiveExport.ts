const messages = {
	en: {
		title: "Selective trace export",
		description:
			"Rules run from highest priority to lowest. Fields inside a rule use AND; values inside one field use OR. The first matching rule decides export for every OTEL profile.",
		ruleRequired: "At least one selection rule is required",
		uniqueRule: "Rule IDs must be unique",
		latencyOrder: "Minimum latency cannot exceed maximum latency",
		errorConflict: "Error categories cannot be combined with a successful final result",
	},
	zh: {
		title: "选择性链路导出",
		description:
			"规则按优先级从高到低执行。单条规则内不同字段使用 AND，同一字段内多个值使用 OR；首个匹配规则决定是否向所有 OTEL 目标导出。",
		ruleRequired: "启用后至少需要一条选择规则",
		uniqueRule: "规则 ID 不能重复",
		latencyOrder: "最小延迟不能大于最大延迟",
		errorConflict: "错误分类不能与“最终结果成功”条件同时使用",
	},
} as const;

export function selectiveExportCopy() {
	const language = typeof document !== "undefined" && document.documentElement.lang.toLowerCase().startsWith("zh") ? "zh" : "en";
	return messages[language];
}