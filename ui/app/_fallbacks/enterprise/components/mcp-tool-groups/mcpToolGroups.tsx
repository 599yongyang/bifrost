import PageTitle from "@/components/pageTitle";
import { ToolCase } from "lucide-react";
import ContactUsView from "../views/contactUsView";
import i18n from "@/lib/i18n";

export default function MCPToolGroups() {
	return (
		<>
			{/* The name and description live in the topbar, like every other page —
			    an inline <h2> here would just repeat the title shown above it. */}
			<PageTitle title={i18n.t("workspace.mcpToolGroups.title")}>{i18n.t("workspace.mcpToolGroups.description")}</PageTitle>
			<div className="rounded-sm border">
				<div className="flex w-full flex-col items-center justify-center py-16">
					<ContactUsView
						className="mx-auto w-full max-w-lg"
						icon={<ToolCase className="h-[5.5rem] w-[5.5rem]" strokeWidth={1} />}
						title={i18n.t("workspace.mcpToolGroups.unlockTitle")}
						description={i18n.t("workspace.enterpriseFallbacks.mcpToolGroupsDescription")}
						readmeLink="https://docs.getbifrost.ai/mcp/overview"
					/>
				</div>
			</div>
		</>
	);
}