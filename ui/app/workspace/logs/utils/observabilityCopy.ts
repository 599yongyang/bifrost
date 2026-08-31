import i18n from "@/lib/i18n";
import type { ObservabilityDisplayStatus } from "./observabilityExport";

const statuses = ["pending", "exported", "not_exported", "failed", "unavailable", "unknown"] as const;
const reasons = [
	"queued",
	"queue_full",
	"missing_input",
	"missing_input_media",
	"missing_output_media",
	"content_hidden",
	"target_unavailable",
	"manual_export_failed",
] as const;

export function observabilityCopy() {
	return {
		export: i18n.t("workspace.logs.observabilityCopy.export"),
		exportPage: i18n.t("workspace.logs.observabilityCopy.exportPage"),
		queued: i18n.t("workspace.logs.observabilityCopy.queued"),
		queuedMany: (count: number) => i18n.t("workspace.logs.observabilityCopy.queuedMany", { count }),
		issues: (accepted: number, issueCount: number) => i18n.t("workspace.logs.observabilityCopy.issues", { accepted, issues: issueCount }),
		statuses: Object.fromEntries(
			statuses.map((status) => [status, i18n.t(`workspace.logs.observabilityCopy.statuses.${status}`)]),
		) as Record<ObservabilityDisplayStatus, string>,
		reasons: Object.fromEntries(reasons.map((reason) => [reason, i18n.t(`workspace.logs.observabilityCopy.reasons.${reason}`)])) as Record<
			string,
			string
		>,
	};
}

export function observationStatusLabel(status: ObservabilityDisplayStatus) {
	return observabilityCopy().statuses[status];
}

export function observationReasonLabel(reason?: string) {
	if (!reason) return "";
	const detailedKey = `workspace.logs.observability.reasons.${reason}`;
	if (i18n.exists(detailedKey)) return i18n.t(detailedKey);
	return observabilityCopy().reasons[reason] ?? reason.replaceAll("_", " ");
}

export function observationReasonGuidance(reason?: string) {
	if (!reason) return "";
	const key = `workspace.logs.observability.guidance.${reason}`;
	return i18n.exists(key) ? i18n.t(key) : "";
}
