import { describe, expect, it } from "vitest";
import { otelFormSchema, type OtelFormSchema } from "@/lib/types/schemas";
import { normalizeSelectiveExportForForm } from "./selectiveExport";

const fallback: OtelFormSchema["selective_export"] = {
	enabled: false,
	dry_run: true,
	require_complete_record: true,
	candidate_rate: 1,
	max_exports_per_minute: 500,
	rules: [
		{
			id: "default",
			priority: 10,
			request_types: [],
			error_categories: [],
			providers: [],
			models: [],
			routing_rules: [],
			export_rate: 0.01,
			max_per_minute: 50,
		},
	],
};

describe("normalizeSelectiveExportForForm", () => {
	it("upgrades a legacy non-atomic config that the current UI cannot edit", () => {
		const normalized = normalizeSelectiveExportForForm(
			{
				enabled: true,
				require_complete_record: false as never,
				rules: [{ id: "errors", priority: 100, require_error: true, export_rate: 1 }],
			},
			fallback,
		);
		expect(normalized.require_complete_record).toBe(true);
		expect(normalized.rules[0]).toMatchObject({
			id: "errors",
			request_types: [],
			error_categories: [],
			providers: [],
			models: [],
			routing_rules: [],
		});
		expect(
			otelFormSchema.safeParse({
				enabled: true,
				profiles: [
					{
						enabled: true,
						service_name: "bifrost",
						collector_url: { type: "plain_text", value: "https://langfuse.example/api/public/otel/v1/traces" },
						headers: {},
						trace_type: "genai_extension",
						protocol: "http",
						tls_ca_cert: "",
						insecure: false,
						metrics_enabled: false,
						metrics_endpoint: { type: "plain_text", value: "" },
						metrics_push_interval: 15,
						export_timeout: 5,
						request_headers: [],
						disable_content_logging: false,
						group_traces_by_session: false,
						disable_root_span_content: false,
					},
				],
				selective_export: normalized,
			}).success,
		).toBe(true);
	});

	it("repairs duplicate and missing hidden IDs while preserving order", () => {
		const normalized = normalizeSelectiveExportForForm(
			{
				enabled: true,
				rules: [
					{ id: "same", priority: 20, export_rate: 1 },
					{ id: "same", priority: 10, export_rate: 0.5 },
					{ priority: 0, export_rate: 0.1 },
				],
			},
			fallback,
		);
		expect(normalized.rules.map((rule) => rule.id)).toEqual(["same", "same-2", "policy-3"]);
		expect(normalized.rules.map((rule) => rule.priority)).toEqual([30, 20, 10]);
	});

	it("restores fallback policies when an enabled legacy config has no rules", () => {
		const normalized = normalizeSelectiveExportForForm({ enabled: true, rules: [] }, fallback);
		expect(normalized.rules).toHaveLength(1);
		expect(normalized.rules[0].id).toBe("default");
	});
});