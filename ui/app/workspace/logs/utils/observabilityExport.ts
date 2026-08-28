import type { LogEntry, ManualObservationExportResponse, ObservationExportStatus } from "@/lib/types/logs";

const manualExportableImageTypes = new Set([
	"image_generation",
	"image_generation_stream",
	"image_edit",
	"image_edit_stream",
	"image_variation",
]);
const irrecoverableManualReasons = new Set([
	"content_hidden",
	"missing_input",
	"missing_input_media",
	"missing_output_media",
	"media_too_large",
	"unsupported_mime",
	"request_type_unsupported",
	"invalid_url",
]);

export type ObservabilityDisplayStatus = ObservationExportStatus["status"] | "unknown";

export function resolveObservabilityExport(log: LogEntry) {
	const states = log.observability_exports ?? [];
	const pending = states.find((item) => item.status === "pending");
	const pendingUpdatedAt = pending ? Date.parse(pending.updated_at) : Number.NaN;
	const stalePending = pending != null && (!Number.isFinite(pendingUpdatedAt) || Date.now() - pendingUpdatedAt > 2 * 60 * 1000);
	const freshPending = stalePending ? undefined : pending;
	const allExported = states.length > 0 && states.every((item) => item.status === "exported");
	const state =
		freshPending ??
		states.find((item) => item.status === "failed") ??
		states.find((item) => item.status === "unavailable") ??
		(stalePending ? pending : undefined) ??
		(allExported
			? states[0]
			: (states.find((item) => item.status === "not_exported") ?? states.find((item) => item.status === "exported")));
	const irrecoverable = states.some(
		(item) => item.source === "manual" && item.status === "unavailable" && irrecoverableManualReasons.has(item.reason ?? ""),
	);
	const configured = log.observability_export_configured === true;
	return {
		state,
		status: !configured ? ("unavailable" as const) : stalePending ? ("failed" as const) : (state?.status ?? ("unknown" as const)),
		manual: allExported && states.some((item) => item.source === "manual"),
		canManualExport:
			configured &&
			log.observability_manual_export_configured === true &&
			manualExportableImageTypes.has(log.object) &&
			log.status !== "processing" &&
			!log.content_hidden &&
			freshPending == null &&
			!allExported &&
			!irrecoverable,
	};
}

export function summarizeManualExportResults(response: ManualObservationExportResponse) {
	const summary = { queued: 0, exported: 0, pending: 0, failed: 0, unavailable: 0 };
	for (const result of response.results) {
		if (result.status === "exported") summary.exported++;
		else if (result.status === "pending") {
			summary.pending++;
			if (result.reason === "queued") summary.queued++;
		} else if (result.status === "unavailable") summary.unavailable++;
		else summary.failed++;
	}
	return summary;
}