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
	media_upload_allowed_origins: [],
};

describe("otel selective export schema", () => {
	it("accepts only exact HTTP media upload origins", () => {
		const valid = otelFormSchema.parse({
			enabled: true,
			profiles: [{ ...profile, media_upload_allowed_origins: ["https://langfuse.tailnet.ts.net:10444"] }],
		});
		expect(valid.profiles[0].media_upload_allowed_origins).toEqual(["https://langfuse.tailnet.ts.net:10444"]);

		for (const origin of [
			"https://langfuse.tailnet.ts.net:10444/upload",
			"https://langfuse.tailnet.ts.net:10444?token=secret",
			"https://user:password@langfuse.tailnet.ts.net:10444",
			"ftp://langfuse.tailnet.ts.net:10444",
		]) {
			expect(
				otelFormSchema.safeParse({ enabled: true, profiles: [{ ...profile, media_upload_allowed_origins: [origin] }] }).success,
			).toBe(false);
		}
	});

	it("serializes the latest atomic media selection fields", () => {
		const parsed = otelFormSchema.parse({
			enabled: true,
			profiles: [profile],
			selective_export: {
				enabled: true,
				dry_run: false,
				require_complete_record: true,
				candidate_rate: 0.75,
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
						min_technical_quality: 0.85,
						export_rate: 0.5,
						max_per_minute: 5,
					},
				],
			},
		});
		const json = JSON.stringify(parsed.selective_export);
		expect(json).toContain('"candidate_rate":0.75');
		expect(json).toContain('"min_technical_quality":0.85');
		expect(json).toContain('"require_complete_record":true');
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
			selective_export: {
				enabled: true,
				dry_run: false,
				require_complete_record: true,
				candidate_rate: 1,
				max_exports_per_minute: 0,
				rules: [rule, { ...rule }],
			},
		});
		expect(result.success).toBe(false);
	});
});
