import { ShieldUser } from "lucide-react";
import ContactUsView from "../views/contactUsView";
import i18n from "@/lib/i18n";

export default function MCPAuthConfigView() {
	return (
		<div className="h-full w-full">
			<ContactUsView
				className="mx-auto min-h-[80vh]"
				icon={<ShieldUser className="h-[5.5rem] w-[5.5rem]" strokeWidth={1} />}
				title={i18n.t("workspace.enterpriseFallbacks.mcpAuthTitle")}
				description={i18n.t("workspace.enterpriseFallbacks.mcpAuthDescription")}
				readmeLink="https://docs.getbifrost.ai/mcp/overview"
			/>
		</div>
	);
}