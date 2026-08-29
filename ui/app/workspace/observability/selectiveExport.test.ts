import { describe, expect, it } from "vitest";
import type { OtelFormSchema } from "@/lib/types/schemas";
import { normalizeSelectiveExportForForm } from "./selectiveExport";

const fallback: OtelFormSchema["selective_export"] = {
	enabled: false,
	dry_run: false,
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
	it("repairs hidden IDs and priorities while retaining policy order", () => {
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

	it("restores the safe template for an enabled legacy config with no rules", () => {
		const normalized = normalizeSelectiveExportForForm({ enabled: true, rules: [] }, fallback);
		expect(normalized.rules).toHaveLength(1);
		expect(normalized.rules[0].id).toBe("default");
	});
});