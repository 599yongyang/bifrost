const CLIPBOARD_UNAVAILABLE_MESSAGE = "Clipboard access is unavailable";

function legacyCopyText(text: string): boolean {
	if (typeof document === "undefined" || !document.body || typeof document.execCommand !== "function") return false;

	const textarea = document.createElement("textarea");
	const previouslyFocused =
		typeof HTMLElement !== "undefined" && document.activeElement instanceof HTMLElement ? document.activeElement : null;
	textarea.value = text;
	textarea.setAttribute("readonly", "");
	Object.assign(textarea.style, {
		position: "fixed",
		inset: "0 auto auto -9999px",
		opacity: "0",
		pointerEvents: "none",
		fontSize: "12pt",
	});
	document.body.appendChild(textarea);

	try {
		textarea.focus({ preventScroll: true });
		textarea.select();
		textarea.setSelectionRange(0, textarea.value.length);
		return document.execCommand("copy");
	} finally {
		textarea.remove();
		previouslyFocused?.focus({ preventScroll: true });
	}
}

/**
 * Copies text across secure and non-secure dashboard deployments.
 * Modern Clipboard API is preferred; the legacy selection path is retained as
 * a compatibility fallback for HTTP deployments and embedded browsers.
 */
export async function copyTextToClipboard(text: string): Promise<void> {
	if (typeof navigator !== "undefined" && navigator.clipboard?.writeText) {
		try {
			await navigator.clipboard.writeText(text);
			return;
		} catch {
			// Permission denial and non-secure contexts can still support the
			// synchronous selection-based fallback while handling the same click.
		}
	}

	if (legacyCopyText(text)) return;
	throw new Error(CLIPBOARD_UNAVAILABLE_MESSAGE);
}