import { afterEach, describe, expect, it } from "vitest";
import { logsPageCopy } from "./logsPageCopy";

const originalDocument = Object.getOwnPropertyDescriptor(globalThis, "document");

afterEach(() => {
	if (originalDocument) Object.defineProperty(globalThis, "document", originalDocument);
	else Reflect.deleteProperty(globalThis, "document");
});

describe("logsPageCopy", () => {
	it("localizes progress, pagination and column labels", () => {
		Object.defineProperty(globalThis, "document", { configurable: true, value: { documentElement: { lang: "zh" } } });
		const copy = logsPageCopy();
		expect(copy.progressSummary(5, 10, 3, 2)).toBe("已检查 5/10 条，更新 3 条，跳过 2 条");
		expect(copy.pageOf(2, 8)).toBe("第 2 页，共 8 页");
		expect(copy.routingRule).toBe("路由规则");
		expect(copy.audioFile).toBe("音频文件");
		expect(copy.failedToRecalculateCosts).toBe("成本重算失败");
		expect(copy.refresh).toBe("刷新");
		expect(copy.noResults).toContain("未找到结果");
		expect(copy.mixed).toBe("混合");
	});
});