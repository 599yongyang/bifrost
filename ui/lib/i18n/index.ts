import i18n from "i18next";
import LanguageDetector from "i18next-browser-languagedetector";
import { initReactI18next } from "react-i18next";

import en from "./locales/en";
import zh from "./locales/zh";

i18n
	.use(LanguageDetector)
	.use(initReactI18next)
	.init({
		resources: {
			en: { translation: en },
			zh: { translation: zh },
		},
		supportedLngs: ["en", "zh"],
		nonExplicitSupportedLngs: true,
		fallbackLng: "en",
		interpolation: {
			escapeValue: false,
		},
		detection: {
			order: ["localStorage", "navigator"],
			caches: ["localStorage"],
			lookupLocalStorage: "bifrost_language",
		},
	});

document.documentElement.lang = i18n.resolvedLanguage ?? i18n.language ?? "en";

i18n.on("languageChanged", (lng) => {
	document.documentElement.lang = lng;
});

export default i18n;