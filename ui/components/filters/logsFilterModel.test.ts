import { describe, expect, it } from "vitest";
import { latencySecondsToMilliseconds } from "./logsFilterModel";

describe("latencySecondsToMilliseconds", () => {
	it("converts seconds to the millisecond API contract", () => {
		expect(latencySecondsToMilliseconds("30")).toBe(30000);
		expect(latencySecondsToMilliseconds("0.125")).toBe(125);
		expect(latencySecondsToMilliseconds("0")).toBe(0);
	});

	it("clears empty, negative and invalid values", () => {
		expect(latencySecondsToMilliseconds("")).toBeUndefined();
		expect(latencySecondsToMilliseconds("-1")).toBeUndefined();
		expect(latencySecondsToMilliseconds("not-a-number")).toBeUndefined();
	});
});