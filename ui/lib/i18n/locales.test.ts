import { describe, expect, it } from "vitest";
import en from "./locales/en";
import zh from "./locales/zh";

function leafKeys(value: unknown, prefix = ""): string[] {
	if (typeof value !== "object" || value === null || Array.isArray(value)) return [prefix];
	return Object.entries(value).flatMap(([key, child]) => leafKeys(child, prefix ? `${prefix}.${key}` : key));
}

describe("i18n locale resources", () => {
	it("keeps English and Chinese key coverage in sync", () => {
		expect(leafKeys(zh).sort()).toEqual(leafKeys(en).sort());
	});

	it("includes fork-specific selective export translations", () => {
		expect(zh.workspace.observability.otelForm.selective.ruleId).toBe("规则 ID");
		expect(zh.workspace.observability.otelForm.selective.minQualityHelp).toContain("不是审美评分");
	});
});