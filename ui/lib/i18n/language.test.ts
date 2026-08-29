import { afterEach, describe, expect, it } from "vitest";
import { getBifrostLanguage, initializeBifrostLanguage, setBifrostLanguage } from "./language";

const originalWindow = Object.getOwnPropertyDescriptor(globalThis, "window");
const originalDocument = Object.getOwnPropertyDescriptor(globalThis, "document");

function installBrowser(language = "en-US") {
	const values = new Map<string, string>();
	Object.defineProperty(globalThis, "window", {
		configurable: true,
		value: {
			navigator: { language },
			localStorage: {
				getItem: (key: string) => values.get(key) ?? null,
				setItem: (key: string, value: string) => values.set(key, value),
			},
		},
	});
	Object.defineProperty(globalThis, "document", {
		configurable: true,
		value: { documentElement: { lang: "en" } },
	});
}

afterEach(() => {
	if (originalWindow) Object.defineProperty(globalThis, "window", originalWindow);
	else Reflect.deleteProperty(globalThis, "window");
	if (originalDocument) Object.defineProperty(globalThis, "document", originalDocument);
	else Reflect.deleteProperty(globalThis, "document");
});

describe("Bifrost language", () => {
	it("uses the browser language until a saved preference overrides it", () => {
		installBrowser("zh-CN");
		expect(initializeBifrostLanguage()).toBe("zh");
		expect(document.documentElement.lang).toBe("zh");
		setBifrostLanguage("en");
		expect(getBifrostLanguage()).toBe("en");
	});
});