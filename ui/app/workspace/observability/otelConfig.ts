import type { OtelFormSchema } from "@/lib/types/schemas";
import { toHeaderStringMap } from "@/lib/utils/secretVarForm";

type OtelProfile = OtelFormSchema["profiles"][number];

export interface OtelPluginConfig {
	profiles: Array<
		Omit<OtelProfile, "headers" | "trace_headers" | "metrics_headers"> & {
			headers: Record<string, string>;
			trace_headers: Record<string, string>;
			metrics_headers: Record<string, string>;
		}
	>;
	selective_export: OtelFormSchema["selective_export"];
}

// Keep every top-level plugin setting in this single serialization boundary so
// profile-only changes cannot silently remove selective export configuration.
export function buildOtelPluginConfig(config: OtelFormSchema): OtelPluginConfig {
	return {
		profiles: config.profiles.map((profile) => ({
			...profile,
			headers: toHeaderStringMap(profile.headers),
			trace_headers: toHeaderStringMap(profile.trace_headers),
			metrics_headers: toHeaderStringMap(profile.metrics_headers),
		})),
		selective_export: config.selective_export,
	};
}