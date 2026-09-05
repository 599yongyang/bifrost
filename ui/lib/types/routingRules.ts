/**
 * Routing Rules Type Definitions
 * Defines all TypeScript interfaces for routing rules feature
 */

import { RuleGroupType } from "react-querybuilder";

export interface RoutingTarget {
	provider?: string;
	model?: string;
	key_id?: string;
	weight: number;
}

export type RoutingErrorFallbackCategory =
	| "content_policy"
	| "unsupported_operation"
	| "rate_limit"
	| "authentication"
	| "billing"
	| "permission"
	| "timeout"
	| "provider_unavailable"
	| "network"
	| "invalid_request"
	| "internal"
	| "unknown";

export interface RoutingErrorFallbackCondition {
	categories?: RoutingErrorFallbackCategory[];
	error_codes?: string[];
	error_types?: string[];
	status_codes?: number[];
	message_contains?: string[];
}

export interface RoutingErrorFallbackSupplement {
	providers?: string[];
	error_codes?: string[];
	error_types?: string[];
	status_codes?: number[];
	message_contains_any?: string[];
}

export interface RoutingErrorFallback {
	name?: string;
	scenario?: RoutingErrorFallbackCategory;
	supplement?: RoutingErrorFallbackSupplement;
	when?: RoutingErrorFallbackCondition;
	fallbacks: string[];
}

export interface RoutingRule {
	id: string;
	name: string;
	description: string;
	cel_expression: string;
	targets: RoutingTarget[];
	fallbacks?: string[];
	error_fallbacks?: RoutingErrorFallback[];
	scope: "global" | "team" | "customer" | "virtual_key" | "user";
	scope_id?: string;
	priority: number;
	enabled: boolean;
	chain_rule: boolean;
	query?: RuleGroupType;
	created_at: string;
	updated_at: string;
}

export interface CreateRoutingRuleRequest {
	name: string;
	description?: string;
	cel_expression?: string;
	targets: RoutingTarget[];
	fallbacks?: string[];
	error_fallbacks?: RoutingErrorFallback[];
	scope: string;
	scope_id?: string;
	priority: number;
	enabled?: boolean;
	chain_rule?: boolean;
	query?: RuleGroupType;
}

/** Partial update: only sent fields are applied; allows clearing fields by sending "" or []. */
export type UpdateRoutingRuleRequest = Partial<CreateRoutingRuleRequest>;

export interface GetRoutingRulesParams {
	limit?: number;
	offset?: number;
	search?: string;
}

export interface GetRoutingRulesResponse {
	rules: RoutingRule[];
	count: number;
	total_count: number;
	limit: number;
	offset: number;
}

export interface GetRoutingRuleResponse {
	rule: RoutingRule;
}

export interface ContentSafetySignalCatalog {
	structured: string[];
	finish_reasons: string[];
	messages: string[];
}

export interface RoutingTargetFormData {
	provider: string;
	model: string;
	key_id: string;
	weight: number;
}

export interface RoutingErrorFallbackConditionFormData {
	categories: RoutingErrorFallbackCategory[];
	error_codes: string[];
	error_types: string[];
	status_codes: number[];
	message_contains: string[];
}

export interface RoutingErrorFallbackSupplementFormData {
	providers: string[];
	error_codes: string[];
	error_types: string[];
	status_codes: number[];
	message_contains_any: string[];
}

export interface RoutingErrorFallbackFormData {
	mode: "scenario" | "legacy";
	originalLegacyRule?: RoutingErrorFallback;
	/** Original content-safety contract retained for lossless round-trips through the simplified editor. */
	originalContentSafetyRule?: RoutingErrorFallback;
	name: string;
	scenario: RoutingErrorFallbackCategory;
	supplement: RoutingErrorFallbackSupplementFormData;
	when: RoutingErrorFallbackConditionFormData;
	fallbacks: string[];
}

export interface RoutingRuleFormData {
	id?: string;
	name: string;
	description: string;
	cel_expression: string;
	targets: RoutingTargetFormData[];
	fallbacks: string[];
	error_fallbacks: RoutingErrorFallbackFormData[];
	scope: string;
	scope_id: string;
	priority: number;
	enabled: boolean;
	chain_rule: boolean;
	query?: RuleGroupType;
	isDirty?: boolean;
}

export enum RoutingRuleScope {
	Global = "global",
	Team = "team",
	Customer = "customer",
	VirtualKey = "virtual_key",
	// Not part of ROUTING_RULE_SCOPES: the sheet offers it only when a user
	// picker is registered (builds with a user directory).
	User = "user",
}

export const ROUTING_RULE_SCOPES = [
	{ value: RoutingRuleScope.Global, label: "Global" },
	{ value: RoutingRuleScope.Team, label: "Team" },
	{ value: RoutingRuleScope.Customer, label: "Customer" },
	{ value: RoutingRuleScope.VirtualKey, label: "Virtual Key" },
];

export const DEFAULT_ROUTING_TARGET: RoutingTargetFormData = {
	provider: "",
	model: "",
	key_id: "",
	weight: 1,
};

export function createDefaultRoutingErrorFallback(): RoutingErrorFallbackFormData {
	return {
		mode: "scenario",
		name: "",
		scenario: "content_policy",
		supplement: {
			providers: [],
			error_codes: [],
			error_types: [],
			status_codes: [],
			message_contains_any: [],
		},
		when: {
			categories: [],
			error_codes: [],
			error_types: [],
			status_codes: [],
			message_contains: [],
		},
		fallbacks: [],
	};
}

export const DEFAULT_ROUTING_RULE_FORM_DATA: RoutingRuleFormData = {
	name: "",
	description: "",
	cel_expression: "",
	targets: [DEFAULT_ROUTING_TARGET],
	fallbacks: [],
	error_fallbacks: [],
	scope: RoutingRuleScope.Global,
	scope_id: "",
	priority: 0,
	enabled: true,
	chain_rule: false,
	isDirty: false,
};