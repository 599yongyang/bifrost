import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

describe("CEL builder responsive layout contract", () => {
	it("keeps rule rows within their container instead of forcing horizontal scrolling", () => {
		const css = readFileSync(new URL("./queryBuilderWrapper.css", import.meta.url), "utf8");
		expect(css).toContain("grid-template-columns: minmax(130px, 180px) minmax(110px, 160px) minmax(140px, 1fr) auto");
		expect(css).toContain("min-width: 0");
		expect(css).toContain("max-width: 100%");
		expect(css).not.toContain("flex-shrink: 0");
	});
});