import type { OtelFormSchema } from "@/lib/types/schemas";
import { toHeaderStringMap } from "@/lib/utils/secretVarForm";

type OtelProfile = OtelFormSchema["profiles"][number];

export interface OtelPluginConfig {
	profiles: Array<Omit<OtelProfile, "headers"> & { headers: Record<string, string> }>;
	selective_export: OtelFormSchema["selective_export"];
}

// Converts the form model into the canonical plugin config sent to the API.
// Keep top-level plugin settings here so adding profile-specific serialization
// cannot silently drop them from persistence.
export function buildOtelPluginConfig(config: OtelFormSchema): OtelPluginConfig {
	return {
		profiles: config.profiles.map((profile) => ({
			...profile,
			headers: toHeaderStringMap(profile.headers),
		})),
		selective_export: config.selective_export,
	};
}
