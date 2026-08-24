export type AlertWindowUnit = "minutes" | "hours" | "days";

export const secondsPerAlertWindowUnit: Record<AlertWindowUnit, number> = {
	minutes: 60,
	hours: 60 * 60,
	days: 24 * 60 * 60,
};

export const maxAlertWindowValue: Record<AlertWindowUnit, number> = {
	minutes: 30 * 24 * 60,
	hours: 30 * 24,
	days: 30,
};

export function alertWindowToSeconds(value: string, unit: AlertWindowUnit): number {
	const numericValue = Math.max(1, Math.floor(Number(value) || 1));
	return numericValue * secondsPerAlertWindowUnit[unit];
}

export function alertCooldownToSeconds(value: string, unit: AlertWindowUnit): number {
	const numericValue = Math.max(0, Math.floor(Number(value) || 0));
	return numericValue * secondsPerAlertWindowUnit[unit];
}

export function alertWindowFromSeconds(seconds: number): { windowValue: string; windowUnit: AlertWindowUnit } {
	const normalized = Math.max(60, Math.floor(seconds || 300));
	if (normalized % secondsPerAlertWindowUnit.days === 0) {
		return { windowValue: String(normalized / secondsPerAlertWindowUnit.days), windowUnit: "days" };
	}
	if (normalized % secondsPerAlertWindowUnit.hours === 0) {
		return { windowValue: String(normalized / secondsPerAlertWindowUnit.hours), windowUnit: "hours" };
	}
	return { windowValue: String(Math.max(1, Math.round(normalized / secondsPerAlertWindowUnit.minutes))), windowUnit: "minutes" };
}

export function alertCooldownFromSeconds(seconds: number): { cooldownValue: string; cooldownUnit: AlertWindowUnit } {
	if (!seconds || seconds <= 0) return { cooldownValue: "0", cooldownUnit: "minutes" };
	const { windowValue, windowUnit } = alertWindowFromSeconds(seconds);
	return { cooldownValue: windowValue, cooldownUnit: windowUnit };
}