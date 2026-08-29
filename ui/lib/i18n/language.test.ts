import { afterEach, describe, expect, it, vi } from "vitest";
import { sidebarLabel } from "@/components/sidebarCopy";
import { topbarCopy } from "@/components/topbarCopy";
import { getBifrostLanguage, initializeBifrostLanguage, localize, setBifrostLanguage } from "./language";

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

	it("selects localized values from the active language", () => {
		installBrowser("zh-CN");
		expect(localize("English", "中文")).toBe("中文");
		setBifrostLanguage("en");
		expect(localize("English", "中文")).toBe("English");
	});

	it("localizes navigation copy after the stored language changes", () => {
		installBrowser("en-US");
		setBifrostLanguage("zh");
		expect(sidebarLabel("Dashboard")).toBe("仪表盘");
		expect(topbarCopy().signOut).toBe("退出登录");
	});

	it("localizes routing builder metadata loaded for the active language", async () => {
		installBrowser("en-US");
		setBifrostLanguage("zh");
		vi.resetModules();

		const [{ baseRoutingFields }, { getOperatorLabel }, { routingRulesCopy }] = await Promise.all([
			import("@/lib/config/celFieldsRouting"),
			import("@/lib/config/celOperatorsRouting"),
			import("@/app/workspace/routing-rules/routingRulesCopy"),
		]);
		expect(baseRoutingFields.find((field) => field.name === "provider")?.label).toBe("供应商");
		expect(getOperatorLabel("contains")).toBe("包含");
		expect(routingRulesCopy.createRule).toBe("新建路由规则");
	});
});
