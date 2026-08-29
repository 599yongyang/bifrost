import { describe, expect, it } from "vitest";
import type { LogEntry } from "@/lib/types/logs";
import { resolveObservabilityExport, summarizeManualExportResults } from "./observabilityExport";

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
	it("keeps partial target failures retryable", () => {
		const result = resolveObservabilityExport({
			...baseLog,
			observability_exports: [
				{ log_id: "log", target_id: "a", status: "failed", source: "automatic", attempts: 1, created_at: "", updated_at: "" },
				{ log_id: "log", target_id: "b", status: "exported", source: "manual", attempts: 2, created_at: "", updated_at: "" },
			],
		});
		expect(result.status).toBe("failed");
		expect(result.manual).toBe(false);
		expect(result.canManualExport).toBe(true);
	});

	it("only exposes manual export for retained, finalized image content", () => {
		expect(resolveObservabilityExport(baseLog).canManualExport).toBe(true);
		expect(resolveObservabilityExport({ ...baseLog, content_hidden: true }).canManualExport).toBe(false);
		expect(resolveObservabilityExport({ ...baseLog, object: "chat.completion" }).canManualExport).toBe(false);
		expect(resolveObservabilityExport({ ...baseLog, status: "processing" }).canManualExport).toBe(false);
	});

	it("allows stale pending jobs but blocks irrecoverable media loss", () => {
		const stale = resolveObservabilityExport({
			...baseLog,
			observability_exports: [
				{
					log_id: "log",
					target_id: "a",
					status: "pending",
					source: "manual",
					attempts: 1,
					created_at: "",
					updated_at: new Date(Date.now() - 180_000).toISOString(),
				},
			],
		});
		expect(stale.status).toBe("failed");
		expect(stale.canManualExport).toBe(true);
		const lost = resolveObservabilityExport({
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
		expect(lost.canManualExport).toBe(false);
	});
});

describe("summarizeManualExportResults", () => {
	it("preserves per-item 202/207 result semantics", () => {
		expect(
			summarizeManualExportResults({
				results: [
					{ id: "a", status: "pending", reason: "queued" },
					{ id: "b", status: "failed", reason: "queue_full" },
					{ id: "c", status: "unavailable", reason: "missing_input" },
				],
			}),
		).toEqual({ queued: 1, exported: 0, pending: 1, failed: 1, unavailable: 1 });
	});
});