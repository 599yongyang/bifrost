import AlertingPlaceholderView from "./alertingPlaceholderView";
import i18n from "@/lib/i18n";

export default function AlertChannelsView() {
	return (
		<AlertingPlaceholderView
			title={i18n.t("workspace.enterpriseFallbacks.alertChannelsTitle")}
			description={i18n.t("workspace.enterpriseFallbacks.alertChannelsDescription")}
			readmeLink="https://docs.getbifrost.ai/enterprise/alerting/alert-channels"
			testIdPrefix="alert-channels"
		/>
	);
}