export function latencySecondsToMilliseconds(value: string): number | undefined {
	if (value === "") return undefined;
	const seconds = Number(value);
	if (!Number.isFinite(seconds) || seconds < 0) return undefined;
	return Math.round(seconds * 1000);
}