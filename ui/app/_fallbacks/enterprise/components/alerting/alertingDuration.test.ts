import { describe, expect, it } from "vitest";
import { alertCooldownFromSeconds, alertCooldownToSeconds, alertWindowFromSeconds, alertWindowToSeconds } from "./alertingDuration";

describe("alert window duration", () => {
	it("converts user-friendly units to API seconds", () => {
		expect(alertWindowToSeconds("5", "minutes")).toBe(300);
		expect(alertWindowToSeconds("2", "hours")).toBe(7200);
		expect(alertWindowToSeconds("3", "days")).toBe(259200);
	});

	it("chooses the clearest unit for an existing rule", () => {
		expect(alertWindowFromSeconds(300)).toEqual({ windowValue: "5", windowUnit: "minutes" });
		expect(alertWindowFromSeconds(7200)).toEqual({ windowValue: "2", windowUnit: "hours" });
		expect(alertWindowFromSeconds(172800)).toEqual({ windowValue: "2", windowUnit: "days" });
	});

	it("represents cooldown zero as no cooldown", () => {
		expect(alertCooldownFromSeconds(0)).toEqual({ cooldownValue: "0", cooldownUnit: "minutes" });
		expect(alertCooldownToSeconds("0", "hours")).toBe(0);
		expect(alertCooldownFromSeconds(3600)).toEqual({ cooldownValue: "1", cooldownUnit: "hours" });
	});
});