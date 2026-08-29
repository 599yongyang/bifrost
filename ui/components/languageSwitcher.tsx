import { Languages } from "lucide-react";
import { useTranslation } from "react-i18next";
import { setBifrostLanguage } from "@/lib/i18n/language";

const languages = [
	{ code: "en", label: "English" },
	{ code: "zh", label: "中文" },
] as const;

export function LanguageSwitcher({ className }: { className?: string }) {
	const { i18n, t } = useTranslation();
	const current = (i18n.resolvedLanguage ?? i18n.language).startsWith("zh") ? "zh" : "en";
	const nextLanguage = languages.find((language) => language.code !== current)?.label ?? "中文";

	const toggleLanguage = () => {
		setBifrostLanguage(current === "zh" ? "en" : "zh");
		// Only the active locale bundle is loaded, so reload to fetch the other
		// language without shipping both bundles on initial page load.
		window.location.reload();
	};

	return (
		<button
			type="button"
			className={`text-muted-foreground hover:bg-accent hover:text-accent-foreground flex size-8 shrink-0 cursor-pointer items-center justify-center rounded-md transition-colors ${className ?? ""}`}
			aria-label={t("languageSwitcher.switchTo", { language: nextLanguage })}
			title={nextLanguage}
			data-testid="language-switcher"
			onClick={toggleLanguage}
		>
			<Languages className="size-4" strokeWidth={2} />
		</button>
	);
}