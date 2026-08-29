import { MonitorSmartphone } from "lucide-react";
import EdgeControlFallbackView from "./fallbackWrapper";
import i18n from "@/lib/i18n";

export default function DevicesView() {
	return (
		<EdgeControlFallbackView
			icon={<MonitorSmartphone className="h-[5.5rem] w-[5.5rem]" strokeWidth={1} />}
			title={i18n.t("workspace.enterpriseFallbacks.edgeDevicesTitle")}
			description={i18n.t("workspace.enterpriseFallbacks.commonDescription")}
			readmeLink="https://docs.getbifrost.ai/edge/admin-devices"
			testIdPrefix="edge-devices"
		/>
	);
}