import { afterEach, describe, expect, it, vi } from "vitest";

import { copyTextToClipboard } from "@/lib/utils/clipboard";

afterEach(() => {
	vi.unstubAllGlobals();
});

function installLegacyClipboard(execResult = true) {
	const textarea = {
		value: "",
		style: {},
		setAttribute: vi.fn(),
		focus: vi.fn(),
		select: vi.fn(),
		setSelectionRange: vi.fn(),
		remove: vi.fn(),
	};
	const appendChild = vi.fn();
	const execCommand = vi.fn(() => execResult);
	vi.stubGlobal("document", {
		activeElement: null,
		body: { appendChild },
		createElement: vi.fn(() => textarea),
		execCommand,
	});
	return { textarea, appendChild, execCommand };
}

describe("copyTextToClipboard", () => {
	it("uses the modern Clipboard API when available", async () => {
		const writeText = vi.fn().mockResolvedValue(undefined);
		vi.stubGlobal("navigator", { clipboard: { writeText } });

		await copyTextToClipboard("trace-id");

		expect(writeText).toHaveBeenCalledWith("trace-id");
	});

	it("falls back to a temporary textarea when Clipboard API permission is denied", async () => {
		const writeText = vi.fn().mockRejectedValue(new DOMException("denied", "NotAllowedError"));
		vi.stubGlobal("navigator", { clipboard: { writeText } });
		const legacy = installLegacyClipboard();

		await copyTextToClipboard("fallback-value");

		expect(legacy.textarea.value).toBe("fallback-value");
		expect(legacy.appendChild).toHaveBeenCalledWith(legacy.textarea);
		expect(legacy.execCommand).toHaveBeenCalledWith("copy");
		expect(legacy.textarea.remove).toHaveBeenCalled();
	});

	it("rejects when neither modern nor legacy copying succeeds", async () => {
		vi.stubGlobal("navigator", {});
		installLegacyClipboard(false);

		await expect(copyTextToClipboard("unavailable")).rejects.toThrow("Clipboard access is unavailable");
	});
});