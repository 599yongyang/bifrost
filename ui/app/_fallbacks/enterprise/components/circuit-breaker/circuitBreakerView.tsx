import { CircuitBoard } from "lucide-react";
import ContactUsView from "../views/contactUsView";
import i18n from "@/lib/i18n";

export default function CircuitBreakerView() {
	return (
		<div className="h-full w-full">
			<ContactUsView
				className="mx-auto min-h-[80vh]"
				icon={<CircuitBoard className="h-[5.5rem] w-[5.5rem]" strokeWidth={1} />}
				title={i18n.t("workspace.enterpriseFallbacks.circuitBreakerTitle")}
				description={i18n.t("workspace.enterpriseFallbacks.circuitBreakerDescription")}
				readmeLink="https://docs.getbifrost.ai/enterprise/circuit-breaker"
			/>
		</div>
	);
}