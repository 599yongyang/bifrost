import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { i18nReady } from "@/lib/i18n";
import type { LogEntry } from "@/lib/types/logs";
import { ObservabilityExportPanel } from "./observabilityExportPanel";

const failedLog = {
	id: "log-1",
	object: "image_edit",
	status: "success",
	timestamp: "2026-08-31T01:30:24Z",
	provider: "openai",
	model: "image",
	number_of_retries: 0,
	fallback_index: 0,
	input_history: [],
	responses_input_history: [],
	stream: false,
	created_at: "2026-08-31T01:30:24Z",
	observability_export_configured: true,
	observability_manual_export_configured: true,
	observability_exports: [
		{
			log_id: "log-1",
			target_id: "otel-target",
			status: "failed",
			source: "manual",
			reason: "media_upload_ssrf_blocked",
			attempts: 1,
			created_at: "2026-08-31T01:31:21Z",
			updated_at: "2026-08-31T01:31:21Z",
		},
	],
} as LogEntry;

describe("ObservabilityExportPanel", () => {
	it("renders a safe actionable manual export failure", async () => {
		await i18nReady;
		const html = renderToStaticMarkup(<ObservabilityExportPanel log={failedLog} onRetry={() => undefined} />);
		expect(html).toContain("Langfuse export");
		expect(html).toContain("untrusted private or Tailscale address");
		expect(html).toContain("exact HTTPS origin");
		expect(html).toContain("Retry export");
		expect(html).not.toContain("X-Amz-Signature");
	});
});
