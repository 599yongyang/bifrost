export type BifrostLanguage = "en" | "zh";

const LANGUAGE_STORAGE_KEY = "bifrost_language";

function normalizeLanguage(value: string | null | undefined): BifrostLanguage | undefined {
	if (!value) return undefined;
	return value.toLowerCase().startsWith("zh") ? "zh" : value.toLowerCase().startsWith("en") ? "en" : undefined;
}

export function getBifrostLanguage(): BifrostLanguage {
	if (typeof document !== "undefined") {
		const documentLanguage = normalizeLanguage(document.documentElement.lang);
		let storedLanguage: BifrostLanguage | undefined;
		try {
			storedLanguage = typeof window !== "undefined" ? normalizeLanguage(window.localStorage.getItem(LANGUAGE_STORAGE_KEY)) : undefined;
		} catch {
			// Storage can be unavailable in privacy-restricted browser contexts.
		}
		const browserLanguage = typeof window !== "undefined" ? normalizeLanguage(window.navigator.language) : undefined;
		return storedLanguage ?? browserLanguage ?? documentLanguage ?? "en";
	}
	return "en";
}

export function initializeBifrostLanguage(): BifrostLanguage {
	const language = getBifrostLanguage();
	if (typeof document !== "undefined") document.documentElement.lang = language;
	return language;
}

export function setBifrostLanguage(language: BifrostLanguage) {
	try {
		if (typeof window !== "undefined") window.localStorage.setItem(LANGUAGE_STORAGE_KEY, language);
	} catch {
		// The current page still switches even when persistence is unavailable.
	}
	if (typeof document !== "undefined") document.documentElement.lang = language;
}

export function localize<T>(english: T, chinese: T): T {
	return getBifrostLanguage() === "zh" ? chinese : english;
}