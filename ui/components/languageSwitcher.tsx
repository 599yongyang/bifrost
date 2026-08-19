import { useTranslation } from "react-i18next";
import { Languages } from "lucide-react";

const languages = [
	{ code: "en", label: "English" },
	{ code: "zh", label: "中文" },
] as const;

export function LanguageSwitcher({ className }: { className?: string }) {
	const { i18n, t } = useTranslation();
	const currentLanguage = (i18n.resolvedLanguage ?? i18n.language).startsWith("zh") ? "zh" : "en";
	const nextLanguage = languages.find((language) => language.code !== currentLanguage)?.label ?? "中文";

	const toggleLanguage = async () => {
		const next = currentLanguage === "zh" ? "en" : "zh";
		await i18n.changeLanguage(next);
		// A few legacy view descriptors translate at module/render boundaries
		// without subscribing to react-i18next. Reloading guarantees every view
		// observes the persisted language instead of leaving mixed-language text.
		window.location.reload();
	};

	return (
		<button
			onClick={() => void toggleLanguage()}
			className={`hover:text-primary text-muted-foreground flex cursor-pointer items-center space-x-3 p-0.5 ${className ?? ""}`}
			type="button"
			aria-label={t("languageSwitcher.switchTo", { language: nextLanguage })}
			title={nextLanguage}
			data-testid="language-switcher"
		>
			<Languages className="h-4 w-4" size={20} strokeWidth={2} />
		</button>
	);
}