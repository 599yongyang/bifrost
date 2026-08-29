import { afterEach, describe, expect, it } from "vitest";
import { DEFAULT_CIRCUIT_BREAKER_POLICY, getSignalMatchMode } from "@/lib/types/circuitBreaker";
import i18n from "./circuitBreakerI18n";

const originalDocument = Object.getOwnPropertyDescriptor(globalThis, "document");

afterEach(() => {
	if (originalDocument) Object.defineProperty(globalThis, "document", originalDocument);
	else Reflect.deleteProperty(globalThis, "document");
});

describe("circuit breaker UI model", () => {
	it("maps each signal shape to the backend match mode", () => {
		expect(getSignalMatchMode({ source: "response_header", header_name: "x-limit" })).toBe("exists");
		expect(getSignalMatchMode({ source: "response_header", header_name: "x-limit", header_value: "true" })).toBe("equals");
		expect(getSignalMatchMode({ source: "response_header", header_name: "x-limit", header_contains: "spill" })).toBe("contains");
	});

	it("starts with a valid editable policy shape", () => {
		expect(DEFAULT_CIRCUIT_BREAKER_POLICY.enabled).toBe(true);
		expect(DEFAULT_CIRCUIT_BREAKER_POLICY.default_cooldown).toBe("30s");
		expect(DEFAULT_CIRCUIT_BREAKER_POLICY.condition.signals).toHaveLength(1);
	});

	it("renders localized and interpolated copy without unresolved keys", () => {
		Object.defineProperty(globalThis, "document", { configurable: true, value: { documentElement: { lang: "zh" } } });
		expect(i18n.t("workspace.circuitBreaker.title")).toBe("熔断器");
		expect(i18n.t("workspace.circuitBreaker.signalNumber", { index: 2 })).toBe("信号 2");
		expect(i18n.t("workspace.circuitBreaker.operatorAny")).toBe("任一");
		expect(i18n.t("common.save")).toBe("保存");
	});
});