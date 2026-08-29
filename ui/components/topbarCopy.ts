import i18n from "@/lib/i18n";

const linkLabelKeys: Record<string, string> = {
	"GitHub Repository": "systemCopy.topbarCopy_github_repository",
	"Full Documentation": "systemCopy.topbarCopy_full_documentation",
};

export const topbarCopy = () => ({
	accountMenu: i18n.t("systemCopy.topbarCopy_account_menu"),
	openMenu: i18n.t("systemCopy.topbarCopy_open_menu"),
	signOut: i18n.t("systemCopy.topbarCopy_sign_out"),
	version: i18n.t("systemCopy.topbarCopy_version"),
});

export function topbarLinkLabel(label: string) {
	return linkLabelKeys[label] ? i18n.t(linkLabelKeys[label], { defaultValue: label }) : label;
}