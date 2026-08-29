import i18n from "@/lib/i18n";

export function selectiveExportCopy() {
	return {
		title: i18n.t("workspace.observability.otelForm.selective.title"),
		description: i18n.t("workspace.observability.otelForm.selective.description"),
		ruleRequired: i18n.t("workspace.observability.otelForm.selective.validationRuleRequired"),
		uniqueRule: i18n.t("workspace.observability.otelForm.selective.validationUniqueRule"),
		latencyOrder: i18n.t("workspace.observability.otelForm.selective.validationLatencyOrder"),
		errorConflict: i18n.t("workspace.observability.otelForm.selective.validationErrorConflict"),
	};
}