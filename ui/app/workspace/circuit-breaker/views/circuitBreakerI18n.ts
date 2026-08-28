import { getBifrostLanguage } from "@/lib/i18n/language";

const en = {
	title: "Circuit Breaker",
	description: "Fail over degraded provider endpoints using explicit response-header signals.",
	active: "active",
	newPolicy: "New policy",
	policy: "Policy",
	trafficPath: "Traffic path",
	signal: "Signal",
	cooldown: "Cooldown",
	status: "Status",
	actions: "Actions",
	search: "Search policies, providers, models, or headers...",
	noMatching: "No circuit breaker policies match this search.",
	keyCircuits: "{{count}} key sub-circuits",
	emptyTitle: "Redirect traffic before degradation becomes an outage",
	emptyDescription:
		"Watch provider response headers, open a circuit when capacity degrades, and route following requests to a known fallback until the primary is ready to probe again.",
	readDocs: "Read docs",
	created: "Circuit breaker policy created",
	updated: "Circuit breaker policy updated",
	deleted: "Circuit breaker policy deleted",
	enabled: "Circuit breaker policy enabled",
	disabled: "Circuit breaker policy disabled",
	deleteTitle: "Delete circuit breaker policy",
	deleteDescription: 'Delete "{{name}}"? Requests will stop using this policy immediately.',
	createPolicy: "Create circuit breaker policy",
	editPolicy: "Edit circuit breaker policy",
	sheetDescription: "Choose the primary route, the degradation signal, and the endpoint that takes over.",
	identity: "Policy identity",
	identityDescription: "Use a stable, unique name so this policy is easy to find in logs.",
	enablePolicy: "Enabled",
	name: "Policy name",
	trafficRoute: "Traffic route",
	trafficRouteDescription: "Requests target the primary until its circuit opens, then move to the fallback.",
	primary: "Primary",
	fallback: "Fallback",
	provider: "Provider",
	model: "Model",
	selectProvider: "Select provider...",
	selectModel: "Select model...",
	keySubCircuits: "Key-level sub-circuits",
	allKeysShared: "Shared circuit for all keys",
	keySubCircuitsDescription:
		"Optional. Degraded keys are excluded individually; the main circuit opens only after every selected key has tripped.",
	signals: "Response signals",
	signalsDescription: "Header names and values are matched case-insensitively.",
	operatorAny: "Any",
	operatorAll: "All",
	signalNumber: "Signal {{index}}",
	removeSignal: "Remove signal",
	headerName: "Header name",
	match: "Match",
	value: "Value",
	exists: "Exists",
	equals: "Equals",
	contains: "Contains",
	anyValue: "Any value",
	addSignal: "Add signal",
	recovery: "Recovery window",
	recoveryDescription: "After cooldown, one request probes the primary while concurrent traffic remains on the fallback.",
	defaultCooldown: "Default cooldown",
	cooldownHeader: "Cooldown header (optional)",
	cooldownHeaderHint: "Header value is interpreted as milliseconds; invalid values fall back to the default.",
	validation: {
		name: "Enter a policy name.",
		uniqueName: "Policy names must be unique.",
		primary: "Choose a primary provider and model.",
		fallback: "Choose a fallback provider and model.",
		differentTarget: "Primary and fallback routes must be different.",
		signal: "Add at least one response signal.",
		headerName: "Every signal needs a header name.",
		headerValue: "Equals signals need a value.",
		headerContains: "Contains signals need a substring.",
		cooldown: "Enter a valid Go duration such as 30s, 5m, or 1h.",
	},
} as const;

const zh = {
	title: "熔断器",
	description: "根据明确的响应头信号，在供应商端点降级时自动切换流量。",
	active: "已启用",
	newPolicy: "新建策略",
	policy: "策略",
	trafficPath: "流量路径",
	signal: "信号",
	cooldown: "冷却时间",
	status: "状态",
	actions: "操作",
	search: "搜索策略、供应商、模型或响应头...",
	noMatching: "没有符合搜索条件的熔断策略。",
	keyCircuits: "{{count}} 个 Key 子熔断器",
	emptyTitle: "在服务降级演变为故障前切走流量",
	emptyDescription: "监控供应商响应头，在容量降级时打开熔断器，并将后续请求路由到备用端点，直到主端点可以再次探测。",
	readDocs: "阅读文档",
	created: "熔断策略已创建",
	updated: "熔断策略已更新",
	deleted: "熔断策略已删除",
	enabled: "熔断策略已启用",
	disabled: "熔断策略已停用",
	deleteTitle: "删除熔断策略",
	deleteDescription: "确定删除「{{name}}」吗？请求将立即停止使用此策略。",
	createPolicy: "创建熔断策略",
	editPolicy: "编辑熔断策略",
	sheetDescription: "设置主路由、降级信号和接管流量的备用端点。",
	identity: "策略标识",
	identityDescription: "使用稳定且唯一的名称，便于在日志中定位此策略。",
	enablePolicy: "启用",
	name: "策略名称",
	trafficRoute: "流量路由",
	trafficRouteDescription: "熔断器打开前请求使用主端点，打开后切换到备用端点。",
	primary: "主端点",
	fallback: "备用端点",
	provider: "供应商",
	model: "模型",
	selectProvider: "选择供应商...",
	selectModel: "选择模型...",
	keySubCircuits: "Key 级子熔断器",
	allKeysShared: "所有 Key 共用熔断器",
	keySubCircuitsDescription: "可选。降级 Key 会被单独排除；只有选中的 Key 全部触发后，主熔断器才会打开。",
	signals: "响应信号",
	signalsDescription: "响应头名称和值均不区分大小写。",
	operatorAny: "任一",
	operatorAll: "全部",
	signalNumber: "信号 {{index}}",
	removeSignal: "移除信号",
	headerName: "响应头名称",
	match: "匹配方式",
	value: "值",
	exists: "存在",
	equals: "等于",
	contains: "包含",
	anyValue: "任意值",
	addSignal: "添加信号",
	recovery: "恢复窗口",
	recoveryDescription: "冷却结束后只放行一个请求探测主端点，并发流量仍走备用端点。",
	defaultCooldown: "默认冷却时间",
	cooldownHeader: "冷却响应头（可选）",
	cooldownHeaderHint: "响应头值按毫秒解析；无效值将使用默认冷却时间。",
	validation: {
		name: "请输入策略名称。",
		uniqueName: "策略名称必须唯一。",
		primary: "请选择主供应商和模型。",
		fallback: "请选择备用供应商和模型。",
		differentTarget: "主路由与备用路由不能相同。",
		signal: "请至少添加一个响应信号。",
		headerName: "每个信号都需要响应头名称。",
		headerValue: "等于匹配需要填写值。",
		headerContains: "包含匹配需要填写子字符串。",
		cooldown: "请输入有效的 Go duration，例如 30s、5m 或 1h。",
	},
};

function getValue(path: string): string {
	if (path.startsWith("common.")) {
		const common =
			getBifrostLanguage() === "zh"
				? { cancel: "取消", delete: "删除", edit: "编辑", save: "保存", saving: "保存中..." }
				: { cancel: "Cancel", delete: "Delete", edit: "Edit", save: "Save", saving: "Saving..." };
		return common[path.slice("common.".length) as keyof typeof common] ?? path;
	}
	const dictionary = getBifrostLanguage() === "zh" ? zh : en;
	const relativePath = path.replace(/^workspace\.circuitBreaker\./, "");
	let value: unknown = dictionary;
	for (const part of relativePath.split(".")) {
		if (!value || typeof value !== "object") return path;
		value = (value as Record<string, unknown>)[part];
	}
	return typeof value === "string" ? value : path;
}

const i18n = {
	t(path: string, variables?: Record<string, unknown>) {
		let value = getValue(path);
		for (const [key, replacement] of Object.entries(variables ?? {})) {
			value = value.replaceAll(`{{${key}}}`, String(replacement ?? ""));
		}
		return value;
	},
};

export default i18n;