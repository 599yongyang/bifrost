import i18n from "@/lib/i18n";

export function logsFilterCopy() {
	return {
		latency: i18n.t("workspace.logs.latencyFilter.title"),
		minimum: i18n.t("workspace.logs.latencyFilter.minimum"),
		maximum: i18n.t("workspace.logs.latencyFilter.maximum"),
		noLimit: i18n.t("workspace.logs.latencyFilter.noLimit"),
		secondsOrMore: (seconds: number) => i18n.t("workspace.logs.latencyFilter.secondsOrMore", { seconds }),
		unitHelp: i18n.t("workspace.logs.latencyFilter.providerUnitHelp"),
		clear: i18n.t("workspace.logs.latencyFilter.clear"),
	};
}