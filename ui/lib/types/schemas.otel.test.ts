import { describe, expect, it } from "vitest";
import { otelFormSchema } from "./schemas";

const profile = {
	enabled: true,
	traces_enabled: true,
	collector_url: { value: "https://otel.example/v1/traces", type: "plain_text" as const },
	trace_type: "genai_extension" as const,
	protocol: "http" as const,
	insecure: true,
	export_timeout: 5,
	metrics_enabled: false,
	metrics_push_interval: 15,
	request_headers: [],
	disable_content_logging: false,
	group_traces_by_session: false,
	disable_root_span_content: false,
};

describe("otel selective export schema", () => {
	it("serializes only fields implemented by the v2 backend", () => {
		const parsed = otelFormSchema.parse({
			enabled: true,
			profiles: [profile],
			selective_export: {
				enabled: true,
				dry_run: false,
				max_exports_per_minute: 20,
				rules: [
					{
						id: "slow-errors",
						priority: 10,
						request_types: ["image_edit"],
						min_latency_ms: 500,
						require_error: true,
						error_categories: ["server_error"],
						providers: ["openai"],
						models: [],
						routing_rules: [],
						min_cost: 0.01,
						export_rate: 0.5,
						max_per_minute: 5,
					},
				],
			},
		});
		const json = JSON.stringify(parsed.selective_export);
		expect(json).not.toContain("candidate_rate");
		expect(json).not.toContain("technical_quality");
		expect(json).not.toContain("require_complete_record");
		expect(parsed.selective_export.rules[0].require_error).toBe(true);
	});

	it("rejects duplicate IDs and invalid latency/error combinations", () => {
		const rule = {
			id: "same",
			priority: 0,
			request_types: [],
			min_latency_ms: 10,
			max_latency_ms: 5,
			require_error: false,
			error_categories: ["timeout"],
			providers: [],
			models: [],
			routing_rules: [],
			export_rate: 1,
			max_per_minute: 0,
		};
		const result = otelFormSchema.safeParse({
			enabled: true,
			profiles: [profile],
			selective_export: { enabled: true, dry_run: false, max_exports_per_minute: 0, rules: [rule, { ...rule }] },
		});
		expect(result.success).toBe(false);
	});
});