import { Badge } from "./ui/badge";
import i18n from "@/lib/i18n";

export default function BetaBadge() {
	return <Badge variant="secondary">{i18n.t("workspace.config.security.beta")}</Badge>;
}