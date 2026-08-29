import type { AlertChannelFormType, AlertChannelRequest, AlertRuleRequest, AlertScopeType } from "@/lib/types/alerting";

export type DurationUnit = "minutes" | "hours" | "days";
const unitSeconds: Record<DurationUnit, number> = { minutes: 60, hours: 3600, days: 86400 };
export const durationToSeconds = (value: string, unit: DurationUnit, allowZero = false) =>
	Math.max(allowZero ? 0 : 1, Math.floor(Number(value) || (allowZero ? 0 : 1))) * unitSeconds[unit];
export function durationFromSeconds(seconds: number, allowZero = false): { value: string; unit: DurationUnit } {
	if (allowZero && seconds <= 0) return { value: "0", unit: "minutes" };
	const normalized = Math.max(60, Math.floor(seconds || 300));
	if (normalized % 86400 === 0) return { value: String(normalized / 86400), unit: "days" };
	if (normalized % 3600 === 0) return { value: String(normalized / 3600), unit: "hours" };
	return { value: String(Math.max(1, Math.round(normalized / 60))), unit: "minutes" };
}

export interface ChannelFormValue {
	name: string;
	description: string;
	type: AlertChannelFormType;
	destination: string;
	headers: string;
	enabled: boolean;
}
export function channelRequest(form: ChannelFormValue, editing = false): AlertChannelRequest {
	const name = form.name.trim();
	if (!name) throw new Error("Channel name is required");
	const destination = form.destination.trim();
	if (!editing && !destination) throw new Error("A destination secret is required");
	const type = form.type;
	const config: Record<string, unknown> = {};
	if (destination && destination !== "***redacted***")
		config[type === "pagerduty" ? "routing_key" : type === "webhook" ? "url" : "webhook_url"] = destination;
	if (type === "webhook" && form.headers.trim()) {
		const headers = JSON.parse(form.headers) as Record<string, unknown>;
		if (!headers || Array.isArray(headers) || Object.values(headers).some((value) => typeof value !== "string"))
			throw new Error("Webhook headers must be a JSON object containing string values");
		config.headers = headers;
	}
	return { name, description: form.description.trim(), type, config, enabled: form.enabled };
}

export interface RuleFormValue {
	name: string;
	description: string;
	enabled: boolean;
	scope_type: AlertScopeType;
	scope_id: string;
	target_type?: "budget" | "model";
	target_id?: string;
	cel_expression: string;
	channel_ids: string[];
	cooldown_seconds: number;
	window_seconds: number;
	min_requests: number;
	notify_once_per_reset_cycle: boolean;
	query?: Record<string, unknown>;
}
export function ruleRequest(form: RuleFormValue): AlertRuleRequest {
	if (!form.name.trim()) throw new Error("Rule name is required");
	if (!form.scope_id.trim()) throw new Error("Scope is required");
	if (!form.cel_expression.trim()) throw new Error("CEL expression is required");
	if (form.channel_ids.length === 0) throw new Error("Select at least one channel");
	if (form.window_seconds < 60 || form.window_seconds > 30 * 86400) throw new Error("Window must be between one minute and 30 days");
	if (!Number.isInteger(form.min_requests) || form.min_requests < 1) throw new Error("Minimum requests must be at least one");
	if (form.cooldown_seconds < 0) throw new Error("Cooldown cannot be negative");
	const targetID = form.target_id?.trim();
	const targetType = targetID ? (form.scope_type === "provider" ? "model" : "budget") : undefined;
	return {
		name: form.name.trim(),
		description: form.description.trim(),
		enabled: form.enabled,
		scope_type: form.scope_type,
		scope_id: form.scope_id.trim(),
		target_type: targetType,
		target_id: targetID || undefined,
		cel_expression: form.cel_expression.trim(),
		channel_ids: [...new Set(form.channel_ids)],
		query: form.query,
		cooldown_milliseconds: form.cooldown_seconds * 1000,
		window_seconds: form.window_seconds,
		min_requests: form.min_requests,
		notify_once_per_reset_cycle: form.notify_once_per_reset_cycle,
	};
}

export function safeHistoryDetail(detail?: string) {
	if (!detail) return "";
	return detail
		.replace(/https?:\/\/\S+/gi, "[redacted-url]")
		.replace(/\b(token|api[_-]?key|secret|authorization|password)\s*[=:]\s*[^\s,;]+/gi, "$1=[redacted]")
		.slice(0, 500);
}