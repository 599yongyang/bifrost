import { afterEach, describe, expect, it } from "vitest";
import { dashboardCopy } from "./dashboardCopy";
import { setTestLanguage } from "@/lib/i18n/testUtils";

const originalDocument = Object.getOwnPropertyDescriptor(globalThis, "document");

afterEach(() => {
	if (originalDocument) Object.defineProperty(globalThis, "document", originalDocument);
	else Reflect.deleteProperty(globalThis, "document");
});

describe("dashboardCopy", () => {
	it("provides Chinese overview chart labels", async () => {
		Object.defineProperty(globalThis, "document", { configurable: true, value: { documentElement: { lang: "zh" } } });
		await setTestLanguage("zh");
		const copy = dashboardCopy();
		expect(copy.requestVolume).toBe("请求量");
		expect(copy.bifrostOverhead).toBe("Bifrost 开销");
		expect(copy.more(3)).toBe("另有 3 项");
	});
});