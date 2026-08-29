import { Palette } from "lucide-react";
import ContactUsView from "../views/contactUsView";
import i18n from "@/lib/i18n";

// OSS stub. Custom branding is an enterprise capability — the OSS backend
// exposes no endpoint to store a logo, so this build always renders the
// Bifrost default and this view only explains the upgrade path.
export default function BrandingView() {
	return (
		<div className="h-full w-full">
			<ContactUsView
				className="mx-auto min-h-[80vh]"
				icon={<Palette className="h-[5.5rem] w-[5.5rem]" strokeWidth={1} />}
				title={i18n.t("workspace.enterpriseFallbacks.brandingTitle")}
				description={i18n.t("workspace.enterpriseFallbacks.commonDescription")}
				readmeLink="https://docs.getbifrost.ai/enterprise/overview"
				testIdPrefix="branding"
			/>
		</div>
	);
}