import { describe, expect, it } from "vitest";
import fs from "node:fs";
import path from "node:path";
import en from "./locales/en";
import zh from "./locales/zh";

function leafKeys(value: unknown, prefix = ""): string[] {
	if (typeof value !== "object" || value === null || Array.isArray(value)) return [prefix];
	return Object.entries(value).flatMap(([key, child]) => leafKeys(child, prefix ? `${prefix}.${key}` : key));
}

function leafValues(value: unknown, prefix = "", out = new Map<string, string>()): Map<string, string> {
	if (typeof value === "string") out.set(prefix, value);
	else if (typeof value === "object" && value !== null && !Array.isArray(value)) {
		for (const [key, child] of Object.entries(value)) leafValues(child, prefix ? `${prefix}.${key}` : key, out);
	}
	return out;
}

function placeholders(value: string): string[] {
	return [...value.matchAll(/{{\s*([^},\s]+)[^}]*}}/g)].map((match) => match[1]).sort();
}

function sourceFiles(directory: string): string[] {
	return fs.readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
		const fullPath = path.join(directory, entry.name);
		if (entry.isDirectory()) return sourceFiles(fullPath);
		return entry.isFile() && /\.tsx?$/.test(entry.name) ? [fullPath] : [];
	});
}

function hasKey(resource: unknown, key: string): boolean {
	let current = resource;
	for (const segment of key.split(".")) {
		if (typeof current !== "object" || current === null || !(segment in current)) return false;
		current = (current as Record<string, unknown>)[segment];
	}
	return typeof current === "string";
}

describe("i18n locale resources", () => {
	it("keeps English and Chinese key coverage in sync", () => {
		expect(leafKeys(zh).sort()).toEqual(leafKeys(en).sort());
	});

	it("includes fork-specific selective export translations", () => {
		expect(zh.workspace.observability.otelForm.selective.ruleId).toBe("规则 ID");
		expect(zh.workspace.observability.otelForm.selective.minQualityHelp).toContain("不是审美评分");
	});

	it("keeps interpolation placeholders aligned", () => {
		const enValues = leafValues(en);
		const zhValues = leafValues(zh);
		for (const [key, enValue] of enValues) expect(placeholders(zhValues.get(key) ?? ""), key).toEqual(placeholders(enValue));
	});

	it("defines every literal i18n key referenced by the UI", () => {
		const root = process.cwd();
		const missing = sourceFiles(root).flatMap((file) => {
			const source = fs.readFileSync(file, "utf8");
			return [...source.matchAll(/i18n\.t\(\s*["']([^"']+)["']/g)]
				.map((match) => match[1])
				.filter((key) => !hasKey(en, key) || !hasKey(zh, key))
				.map((key) => `${path.relative(root, file)}: ${key}`);
		});
		expect(missing).toEqual([]);
	});
});
