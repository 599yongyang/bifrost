import AlertingPlaceholderView from "./alertingPlaceholderView";
import i18n from "@/lib/i18n";

export default function AlertHistoryView() {
	return (
		<AlertingPlaceholderView
			title={i18n.t("workspace.enterpriseFallbacks.alertHistoryTitle")}
			description={i18n.t("workspace.enterpriseFallbacks.alertHistoryDescription")}
			readmeLink="https://docs.getbifrost.ai/enterprise/alerting/alert-history"
			testIdPrefix="alert-history"
		/>
	);
}