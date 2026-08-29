import AlertingPlaceholderView from "./alertingPlaceholderView";
import i18n from "@/lib/i18n";

export default function AlertRulesView() {
	return (
		<AlertingPlaceholderView
			title={i18n.t("workspace.enterpriseFallbacks.alertRulesTitle")}
			description={i18n.t("workspace.enterpriseFallbacks.alertRulesDescription")}
			readmeLink="https://docs.getbifrost.ai/enterprise/alerting/alert-rules"
			testIdPrefix="alert-rules"
		/>
	);
}