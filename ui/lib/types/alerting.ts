export type AlertChannelType = "slack" | "microsoft_teams" | "wecom" | "pagerduty" | "webhook";
export type AlertChannelFormType = AlertChannelType;
export type AlertScopeType = "virtual_key" | "team" | "customer" | "provider";
export type AlertStatus = "sent" | "failed" | "skipped";

export interface AlertChannel {
	id: string;
	name: string;
	description?: string;
	type: AlertChannelType;
	enabled: boolean;
	config: Record<string, unknown>;
	created_at: string;
	updated_at: string;
}
export type AlertChannelRequest = Omit<AlertChannel, "id" | "created_at" | "updated_at">;
export interface AlertRule {
	id: string;
	name: string;
	description?: string;
	enabled: boolean;
	scope_type: AlertScopeType;
	scope_id: string;
	target_type?: "budget" | "model";
	target_id?: string;
	cel_expression: string;
	channel_ids: string[];
	query?: Record<string, unknown>;
	cooldown_milliseconds: number;
	window_seconds: number;
	min_requests: number;
	notify_once_per_reset_cycle: boolean;
	created_at: string;
	updated_at: string;
}
export type AlertRuleRequest = Omit<AlertRule, "id" | "created_at" | "updated_at">;
export interface AlertRuleEvaluationResult {
	rule_id: string;
	matched: boolean;
	matched_targets: number;
	sent_count: number;
	skipped_count: number;
	failed_count: number;
	cooldown_ignored: boolean;
}
export interface AlertHistoryRecord {
	id: string;
	rule_id: string;
	rule_name: string;
	channel_id?: string;
	channel_name?: string;
	channel_type?: AlertChannelType;
	scope_type: AlertScopeType;
	scope_id: string;
	target_type?: string;
	target_id?: string;
	cel_expression: string;
	input: Record<string, unknown>;
	status: AlertStatus;
	status_detail?: string;
	created_at: string;
}
export interface AlertHistoryParams {
	limit?: number;
	offset?: number;
	status?: AlertStatus[];
	scope_type?: AlertScopeType[];
	channel_type?: AlertChannelType[];
}