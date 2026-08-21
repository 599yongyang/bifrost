import { describe, expect, it } from "vitest";
import type { LogEntry } from "@/lib/types/logs";
import { resolveObservabilityExport } from "./observabilityExport";

const baseLog = {
	id: "log",
	object: "image_edit",
	status: "success",
	timestamp: "",
	provider: "openai",
	model: "image",
	number_of_retries: 0,
	fallback_index: 0,
	input_history: [],
	responses_input_history: [],
	stream: false,
	created_at: "",
	observability_export_configured: true,
	observability_manual_export_configured: true,
} as LogEntry;

describe("resolveObservabilityExport", () => {
	it("prioritizes an exported target and identifies manual exports", () => {
		const result = resolveObservabilityExport({
			...baseLog,
			observability_exports: [
				{ log_id: "log", target_id: "a", status: "failed", source: "automatic", attempts: 1, created_at: "", updated_at: "" },
				{ log_id: "log", target_id: "b", status: "exported", source: "manual", attempts: 2, created_at: "", updated_at: "" },
			],
		});
		expect(result.status).toBe("exported");
		expect(result.manual).toBe(true);
		expect(result.canManualExport).toBe(false);
	});

	it("allows retained image logs with unknown or failed status to be exported", () => {
		expect(resolveObservabilityExport(baseLog).canManualExport).toBe(true);
		expect(
			resolveObservabilityExport({
				...baseLog,
				observability_exports: [
					{ log_id: "log", target_id: "a", status: "failed", source: "automatic", attempts: 1, created_at: "", updated_at: "" },
				],
			}).canManualExport,
		).toBe(true);
	});

	it("rejects hidden content and non-image request types", () => {
		expect(resolveObservabilityExport({ ...baseLog, content_hidden: true }).canManualExport).toBe(false);
		expect(resolveObservabilityExport({ ...baseLog, object: "chat.completion" }).canManualExport).toBe(false);
		expect(resolveObservabilityExport({ ...baseLog, status: "processing" }).canManualExport).toBe(false);
	});

	it("does not offer export when no Langfuse target is configured", () => {
		const result = resolveObservabilityExport({ ...baseLog, observability_export_configured: false });
		expect(result.status).toBe("unavailable");
		expect(result.canManualExport).toBe(false);
	});

	it("shows status but disables manual export when content export is disabled", () => {
		const result = resolveObservabilityExport({ ...baseLog, observability_manual_export_configured: false });
		expect(result.status).toBe("unknown");
		expect(result.canManualExport).toBe(false);
	});

	it("allows a stale pending job to be retried", () => {
		const result = resolveObservabilityExport({
			...baseLog,
			observability_exports: [
				{
					log_id: "log",
					target_id: "a",
					status: "pending",
					source: "manual",
					attempts: 1,
					created_at: "",
					updated_at: new Date(Date.now() - 3 * 60 * 1000).toISOString(),
				},
			],
		});
		expect(result.status).toBe("failed");
		expect(result.canManualExport).toBe(true);
	});

	it("does not offer retries for irrecoverable manual media loss", () => {
		const result = resolveObservabilityExport({
			...baseLog,
			observability_exports: [
				{
					log_id: "log",
					target_id: "a",
					status: "unavailable",
					source: "manual",
					reason: "missing_input_media",
					attempts: 1,
					created_at: "",
					updated_at: new Date().toISOString(),
				},
			],
		});
		expect(result.canManualExport).toBe(false);
	});
});