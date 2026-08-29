import { getBifrostLanguage, setBifrostLanguage } from "@/lib/i18n/language";
import { Languages } from "lucide-react";

export function LanguageSwitcher() {
	const current = getBifrostLanguage();
	const next = current === "zh" ? "en" : "zh";
	const label = current === "zh" ? "Switch to English" : "切换到中文";

	return (
		<button
			type="button"
			className="text-muted-foreground hover:bg-accent hover:text-accent-foreground flex size-8 shrink-0 cursor-pointer items-center justify-center rounded-md transition-colors"
			aria-label={label}
			title={label}
			data-testid="language-switcher"
			onClick={() => {
				setBifrostLanguage(next);
				window.location.reload();
			}}
		>
			<Languages className="size-4" strokeWidth={2} />
		</button>
	);
}