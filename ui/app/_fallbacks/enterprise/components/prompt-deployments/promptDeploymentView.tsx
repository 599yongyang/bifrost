import { Router } from "lucide-react";
import ContactUsView from "../views/contactUsView";
import i18n from "@/lib/i18n";

export default function PromptDeploymentView(_props?: { omitTitle?: boolean }) {
	return (
		<div className="w-full">
			<ContactUsView
				align="top"
				className="justify-start gap-3 rounded-md border p-4"
				icon={<Router className="h-8 w-8" strokeWidth={1.5} />}
				title={i18n.t("workspace.enterpriseFallbacks.promptDeploymentsTitle")}
				description={i18n.t("workspace.enterpriseFallbacks.commonDescription")}
				readmeLink="https://docs.getbifrost.ai/enterprise/prompt-deployments"
			/>
		</div>
	);
}