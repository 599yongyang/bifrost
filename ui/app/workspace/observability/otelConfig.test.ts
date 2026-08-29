import { describe, expect, it } from "vitest";
import type { OtelFormSchema } from "@/lib/types/schemas";
import { buildOtelPluginConfig } from "./otelConfig";

describe("buildOtelPluginConfig", () => {
	it("persists selective export and serializes every header group", () => {
		const form = {
			enabled: true,
			profiles: [
				{
					enabled: true,
					traces_enabled: true,
					service_name: "bifrost",
					collector_url: { value: "https://otel.example/v1/traces", type: "plain_text" },
					headers: { Authorization: { value: "", ref: "env.OTEL_TOKEN", type: "env" } },
					trace_headers: { "x-trace": { value: "trace", type: "plain_text" } },
					metrics_headers: { "x-metrics": { value: "metrics", type: "plain_text" } },
					trace_type: "genai_extension",
					protocol: "http",
					tls_ca_cert: "",
					insecure: false,
					metrics_enabled: false,
					metrics_endpoint: { value: "", type: "plain_text" },
					metrics_push_interval: 15,
					export_timeout: 5,
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
				max_exports_per_minute: 500,
				rules: [
					{
						id: "errors",
						priority: 100,
						request_types: [],
						require_error: true,
						error_categories: [],
						providers: [],
						models: [],
						routing_rules: [],
						export_rate: 1,
						max_per_minute: 100,
					},
				],
			},
		} satisfies OtelFormSchema;

		const payload = buildOtelPluginConfig(form);

		expect(payload.selective_export).toEqual(form.selective_export);
		expect(payload.profiles[0].headers).toEqual({ Authorization: "env.OTEL_TOKEN" });
		expect(payload.profiles[0].trace_headers).toEqual({ "x-trace": "trace" });
		expect(payload.profiles[0].metrics_headers).toEqual({ "x-metrics": "metrics" });
	});
});