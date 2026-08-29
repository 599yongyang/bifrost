import { Users } from "lucide-react";
import ContactUsView from "../views/contactUsView";
import i18n from "@/lib/i18n";

export default function UserRankingsTab() {
	return (
		<div className="h-full w-full">
			<ContactUsView
				className="mx-auto min-h-[80vh]"
				icon={<Users className="h-[5.5rem] w-[5.5rem]" strokeWidth={1} />}
				title={i18n.t("workspace.enterpriseFallbacks.userRankingsTitle")}
				description={i18n.t("workspace.enterpriseFallbacks.commonDescription")}
				readmeLink="https://docs.getbifrost.ai/enterprise/user-rankings"
				testIdPrefix="user-rankings"
			/>
		</div>
	);
}