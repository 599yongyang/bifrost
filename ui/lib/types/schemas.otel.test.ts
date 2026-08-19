import { describe, expect, it } from "vitest";
import { otelFormSchema, otelSelectiveExportSchema } from "./schemas";

describe("otel selective export schema", () => {
	it("accepts ordered image classification rules", () => {
		const result = otelSelectiveExportSchema.safeParse({
			enabled: true,
			dry_run: true,
			require_complete_record: true,
			candidate_rate: 1,
			max_exports_per_minute: 500,
			rules: [
				{
					id: "slow",
					priority: 80,
					request_types: ["image_generation"],
					min_latency_ms: 30000,
					export_rate: 0.3,
					max_per_minute: 100,
				},
				{
					id: "default",
					priority: 0,
					request_types: [],
					export_rate: 0.01,
					max_per_minute: 50,
				},
			],
		});
		expect(result.success).toBe(true);
	});

	it("rejects duplicate ids and invalid percentages", () => {
		const result = otelSelectiveExportSchema.safeParse({
			enabled: true,
			dry_run: false,
			require_complete_record: true,
			candidate_rate: 1,
			max_exports_per_minute: 100,
			rules: [
				{
					id: "same",
					priority: 2,
					request_types: [],
					export_rate: 1.2,
					max_per_minute: 10,
				},
				{
					id: "same",
					priority: 1,
					request_types: [],
					export_rate: 0.5,
					max_per_minute: 10,
				},
			],
		});
		expect(result.success).toBe(false);
	});

	it("requires atomic complete records", () => {
		const result = otelSelectiveExportSchema.safeParse({
			enabled: true,
			dry_run: false,
			require_complete_record: false,
			candidate_rate: 1,
			max_exports_per_minute: 10,
			rules: [{ id: "all", priority: 0, request_types: [], export_rate: 1, max_per_minute: 0 }],
		});
		expect(result.success).toBe(false);
	});

	it("requires one HTTP content-enabled profile when selection is enabled", () => {
		const base = {
			enabled: true,
			profiles: [
				{
					enabled: true,
					service_name: "bifrost",
					collector_url: { type: "plain_text", value: "https://langfuse.example/api/public/otel/v1/traces" },
					headers: {},
					trace_type: "genai_extension",
					protocol: "grpc",
					insecure: false,
					metrics_enabled: false,
					request_headers: [],
					disable_content_logging: false,
					group_traces_by_session: false,
					disable_root_span_content: false,
				},
			],
			selective_export: {
				enabled: true,
				dry_run: false,
				require_complete_record: true,
				candidate_rate: 1,
				max_exports_per_minute: 10,
				rules: [{ id: "all", priority: 0, request_types: [], export_rate: 1, max_per_minute: 0 }],
			},
		};
		expect(otelFormSchema.safeParse(base).success).toBe(false);
		expect(otelFormSchema.safeParse({ ...base, profiles: [{ ...base.profiles[0], protocol: "http" }] }).success).toBe(true);
	});
});