import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import { getBifrostLanguage } from "./language";

const language = getBifrostLanguage();

export const i18nReady = (language === "zh" ? import("./locales/zh") : import("./locales/en")).then(async ({ default: translation }) => {
	await i18n.use(initReactI18next).init({
		resources: {
			[language]: { translation },
		},
		lng: language,
		supportedLngs: ["en", "zh"],
		nonExplicitSupportedLngs: true,
		// Locale parity tests guarantee both languages contain the same keys, so
		// the inactive locale does not need to be downloaded as a fallback.
		fallbackLng: language,
		interpolation: {
			escapeValue: false,
		},
	});
	if (typeof document !== "undefined") document.documentElement.lang = language;
});

i18n.on("languageChanged", (lng) => {
	if (typeof document !== "undefined") document.documentElement.lang = lng;
});

export default i18n;