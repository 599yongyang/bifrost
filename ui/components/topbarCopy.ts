import { localize } from "@/lib/i18n/language";

const linkLabels: Record<string, string> = {
	"GitHub Repository": "GitHub 仓库",
	"Full Documentation": "完整文档",
};

export const topbarCopy = () => ({
	accountMenu: localize("Account menu", "账户菜单"),
	openMenu: localize("Open menu", "打开菜单"),
	signOut: localize("Sign out", "退出登录"),
	version: localize("Version", "版本"),
});

export function topbarLinkLabel(label: string) {
	return localize(label, linkLabels[label] ?? label);
}