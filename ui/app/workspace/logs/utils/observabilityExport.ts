import type { LogEntry, ObservationExportStatus } from "@/lib/types/logs";

const manualExportableImageTypes = new Set([
	"image_generation",
	"image_generation_stream",
	"image_edit",
	"image_edit_stream",
	"image_variation",
]);
const statusPriority: ObservationExportStatus["status"][] = ["exported", "pending", "failed", "unavailable", "not_exported"];
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

export function resolveObservabilityExport(log: LogEntry) {
	const states = log.observability_exports ?? [];
	const state = statusPriority.map((status) => states.find((item) => item.status === status)).find(Boolean);
	const updatedAt = state ? Date.parse(state.updated_at) : Number.NaN;
	const stalePending = state?.status === "pending" && (!Number.isFinite(updatedAt) || Date.now() - updatedAt > 2 * 60 * 1000);
	const irrecoverable = state?.source === "manual" && state.status === "unavailable" && irrecoverableManualReasons.has(state.reason ?? "");
	const configured = log.observability_export_configured === true;
	return {
		state,
		status: !configured ? ("unavailable" as const) : stalePending ? ("failed" as const) : (state?.status ?? ("unknown" as const)),
		manual: state?.status === "exported" && state.source === "manual",
		canManualExport:
			configured &&
			log.observability_manual_export_configured === true &&
			manualExportableImageTypes.has(log.object) &&
			log.status !== "processing" &&
			!log.content_hidden &&
			(state?.status !== "pending" || stalePending) &&
			state?.status !== "exported" &&
			!irrecoverable,
	};
}