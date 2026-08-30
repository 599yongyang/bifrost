import { describe, expect, it } from "vitest";
import { isReleaseDismissed, shouldShowReleaseNotice } from "./releases";

describe("shouldShowReleaseNotice", () => {
	it("does not treat a Moon build as older than its matching upstream base", () => {
		expect(shouldShowReleaseNotice("2.0.0", "2.0.0-moon.12")).toBe(false);
		expect(shouldShowReleaseNotice("v2.0.0", "v2.0.0-moon.12")).toBe(false);
	});

	it("still reports a newer upstream patch, minor, or major release", () => {
		expect(shouldShowReleaseNotice("2.0.1", "2.0.0-moon.12")).toBe(true);
		expect(shouldShowReleaseNotice("2.1.0", "2.0.0-moon.12")).toBe(true);
		expect(shouldShowReleaseNotice("3.0.0", "2.0.0-moon.12")).toBe(true);
	});

	it("reports v2 to older Moon installations", () => {
		expect(shouldShowReleaseNotice("2.0.0", "1.6.10-moon.31")).toBe(true);
	});

	it("does not notify when either version is invalid", () => {
		expect(shouldShowReleaseNotice("2.0.0", "unknown")).toBe(false);
		expect(shouldShowReleaseNotice("", "2.0.0-moon.12")).toBe(false);
	});
});

describe("isReleaseDismissed", () => {
	it("matches a dismissed release regardless of a leading v", () => {
		expect(isReleaseDismissed("v2.0.1", "2.0.1")).toBe(true);
		expect(isReleaseDismissed("2.0.1", "v2.0.1")).toBe(true);
	});

	it("allows a newer release to be shown", () => {
		expect(isReleaseDismissed("2.0.2", "2.0.1")).toBe(false);
	});
});