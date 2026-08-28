import type { ObservabilityDisplayStatus } from "./observabilityExport";

const copy = {
	en: {
		export: "Export to observability",
		exportPage: "Export eligible page",
		queued: "Observability export queued.",
		queuedMany: (count: number) => `${count} observability exports queued.`,
		issues: (accepted: number, issues: number) => `Export queued with issues: ${accepted} accepted, ${issues} unavailable.`,
		statuses: {
			pending: "Pending",
			exported: "Exported",
			not_exported: "Not exported",
			failed: "Failed",
			unavailable: "Unavailable",
			unknown: "Not evaluated",
		},
		reasons: {
			queued: "Queued",
			queue_full: "Export queue is full",
			missing_input: "Input content is unavailable",
			missing_input_media: "Input media is unavailable",
			missing_output_media: "Output media is unavailable",
			content_hidden: "Content was not retained",
			target_unavailable: "No compatible target is available",
			manual_export_failed: "Export failed",
		},
	},
	zh: {
		export: "导出到可观测平台",
		exportPage: "导出本页可用记录",
		queued: "可观测导出已加入队列。",
		queuedMany: (count: number) => `${count} 条可观测导出已加入队列。`,
		issues: (accepted: number, issues: number) => `导出已排队：${accepted} 条已接受，${issues} 条不可用。`,
		statuses: { pending: "等待中", exported: "已导出", not_exported: "未导出", failed: "失败", unavailable: "不可用", unknown: "未评估" },
		reasons: {
			queued: "已加入队列",
			queue_full: "导出队列已满",
			missing_input: "输入内容不可用",
			missing_input_media: "输入媒体不可用",
			missing_output_media: "输出媒体不可用",
			content_hidden: "内容未保留",
			target_unavailable: "没有可用目标",
			manual_export_failed: "导出失败",
		},
	},
} as const;

export function observabilityCopy() {
	const language = typeof document !== "undefined" && document.documentElement.lang.toLowerCase().startsWith("zh") ? "zh" : "en";
	return copy[language];
}

export function observationStatusLabel(status: ObservabilityDisplayStatus) {
	return observabilityCopy().statuses[status];
}

export function observationReasonLabel(reason?: string) {
	if (!reason) return "";
	const reasons = observabilityCopy().reasons as Record<string, string>;
	return reasons[reason] ?? reason.replaceAll("_", " ");
}