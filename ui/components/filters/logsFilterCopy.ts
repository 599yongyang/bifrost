const content = {
	en: {
		latency: "Latency",
		minimum: "Minimum (seconds)",
		maximum: "Maximum (seconds)",
		noLimit: "No limit",
		secondsOrMore: (seconds: number) => `${seconds}s or more`,
		unitHelp: "Provider response latency; decimals are supported.",
		clear: "Clear",
	},
	zh: {
		latency: "延迟",
		minimum: "最小值（秒）",
		maximum: "最大值（秒）",
		noLimit: "不限",
		secondsOrMore: (seconds: number) => `${seconds} 秒及以上`,
		unitHelp: "供应商响应延迟，支持小数。",
		clear: "清除",
	},
} as const;

export function logsFilterCopy() {
	return typeof document !== "undefined" && document.documentElement.lang.toLowerCase().startsWith("zh") ? content.zh : content.en;
}