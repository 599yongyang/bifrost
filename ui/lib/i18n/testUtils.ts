import i18n, { i18nReady } from "./index";
import en from "./locales/en";
import zh from "./locales/zh";
import type { BifrostLanguage } from "./language";

export async function setTestLanguage(language: BifrostLanguage) {
	await i18nReady;
	i18n.addResourceBundle("en", "translation", en, true, true);
	i18n.addResourceBundle("zh", "translation", zh, true, true);
	await i18n.changeLanguage(language);
	if (typeof document !== "undefined") document.documentElement.lang = language;
}