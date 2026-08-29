import i18n from "@/lib/i18n";

type Variables = Record<string, string | number>;

export function selectiveExportText(key: string, variables: Variables = {}): string {
	return i18n.t(key, variables);
}