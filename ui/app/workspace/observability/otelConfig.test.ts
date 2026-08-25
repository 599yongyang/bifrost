import { describe, expect, it } from "vitest";
import type { OtelFormSchema } from "@/lib/types/schemas";
import { buildOtelPluginConfig } from "./otelConfig";

describe("buildOtelPluginConfig", () => {
	it("persists the enabled selective export strategy", () => {
		const form = {
			enabled: true,
			profiles: [
				{
					enabled: true,
					service_name: "bifrost",
					collector_url: { value: "https://langfuse.example/api/public/otel", type: "plain_text" },
					headers: {},
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
		expect(payload.selective_export.enabled).toBe(true);
	});
});
