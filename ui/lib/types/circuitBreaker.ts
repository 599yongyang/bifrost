export type CircuitBreakerOperator = "OR" | "AND";
export type CircuitBreakerMatchMode = "exists" | "equals" | "contains";

export interface CircuitBreakerSignal {
	source: "response_header";
	header_name: string;
	header_value?: string;
	header_contains?: string;
}

export interface CircuitBreakerCondition {
	operator?: CircuitBreakerOperator;
	signals: CircuitBreakerSignal[];
}

export interface CircuitBreakerPolicy {
	name: string;
	enabled?: boolean;
	primary_provider: string;
	primary_model: string;
	primary_key_ids?: string[];
	fallback_provider: string;
	fallback_model: string;
	condition: CircuitBreakerCondition;
	default_cooldown?: string;
	cooldown_header?: string;
}

export interface CircuitBreakerConfig {
	policies: CircuitBreakerPolicy[];
}

export const CIRCUIT_BREAKER_PLUGIN = "circuit-breaker";

export const DEFAULT_CIRCUIT_BREAKER_POLICY: CircuitBreakerPolicy = {
	name: "",
	enabled: true,
	primary_provider: "",
	primary_model: "",
	primary_key_ids: [],
	fallback_provider: "",
	fallback_model: "",
	condition: {
		operator: "OR",
		signals: [{ source: "response_header", header_name: "" }],
	},
	default_cooldown: "30s",
};

export function getSignalMatchMode(signal: CircuitBreakerSignal): CircuitBreakerMatchMode {
	if (signal.header_value !== undefined) return "equals";
	if (signal.header_contains !== undefined) return "contains";
	return "exists";
}