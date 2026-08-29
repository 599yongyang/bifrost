/**
 * CEL Fields Configuration for Routing Rules
 * Defines available fields for building routing rule expressions
 */

import { getProviderLabel } from "@/lib/constants/logs";
import { COMPLEXITY_TIER_VALUES } from "@/lib/types/complexityRouter";
import i18n from "@/lib/i18n";

export interface CELFieldDefinition {
	name: string;
	label: string;
	placeholder?: string;
	inputType?: "text" | "select" | "keyValue" | "number";
	valueEditorType?:
		| "text"
		| "select"
		| "keyValue"
		| "number"
		| "textarea"
		| "budgetNumber"
		| ((operator: string) => "text" | "select" | "keyValue" | "number" | "textarea" | "budgetNumber");
	operators?: string[];
	defaultOperator?: string;
	defaultValue?: any;
	values?: Array<{ name: string; label: string; disabled?: boolean }>;
	metricOptions?: Array<{ name: string; label: string }>; // For budgetNumber type
	description?: string; // Helpful note for the user
}

export const baseRoutingFields: CELFieldDefinition[] = [
	{
		name: "model",
		label: i18n.t("workspace.routingRules.copy.celFieldsRouting_model"),
		placeholder: i18n.t("workspace.routingRules.copy.celFieldsRouting_e_g_gpt_4_claude_3_sonnet"),
		inputType: "text",
		valueEditorType: (operator: string) =>
			operator === "=" || operator === "!=" ? "select" : operator === "in" || operator === "notIn" ? "select" : "text",
		operators: ["=", "!=", "in", "notIn", "contains", "beginsWith", "endsWith", "matches"],
		defaultOperator: "=",
	},
	{
		name: "provider",
		label: i18n.t("workspace.routingRules.copy.celFieldsRouting_provider"),
		placeholder: i18n.t("workspace.routingRules.copy.celFieldsRouting_select_provider"),
		inputType: "select",
		valueEditorType: (operator: string) =>
			operator === "matches" ? "text" : operator === "in" || operator === "notIn" ? "select" : "select",
		operators: ["=", "!=", "in", "notIn", "matches", "null", "notNull"],
		defaultOperator: "=",
	},
	{
		name: "request_type",
		label: i18n.t("workspace.routingRules.copy.celFieldsRouting_request_type"),
		placeholder: i18n.t("workspace.routingRules.copy.celFieldsRouting_select_request_type"),
		inputType: "select",
		valueEditorType: (operator: string) =>
			operator === "matches" ? "text" : operator === "in" || operator === "notIn" ? "select" : "select",
		operators: ["=", "!=", "in", "notIn", "matches"],
		defaultOperator: "=",
		values: [
			{ name: "text_completion", label: i18n.t("workspace.routingRules.copy.celFieldsRouting_text_completion") },
			{ name: "text_completion_stream", label: i18n.t("workspace.routingRules.copy.celFieldsRouting_text_completion_streaming") },
			{ name: "chat_completion", label: i18n.t("workspace.routingRules.copy.celFieldsRouting_chat_completion") },
			{ name: "chat_completion_stream", label: i18n.t("workspace.routingRules.copy.celFieldsRouting_chat_completion_streaming") },
			{ name: "responses", label: "Responses" },
			{ name: "responses_stream", label: i18n.t("workspace.routingRules.copy.celFieldsRouting_responses_streaming") },
			{ name: "embedding", label: i18n.t("workspace.routingRules.copy.celFieldsRouting_embeddings") },
			{ name: "image_generation", label: i18n.t("workspace.routingRules.copy.celFieldsRouting_image_generation") },
			{ name: "image_generation_stream", label: i18n.t("workspace.routingRules.copy.celFieldsRouting_image_generation_streaming") },
			{ name: "image_edit", label: i18n.t("workspace.routingRules.copy.celFieldsRouting_image_edit") },
			{ name: "image_edit_stream", label: i18n.t("workspace.routingRules.copy.celFieldsRouting_image_edit_streaming") },
			{ name: "image_variation", label: i18n.t("workspace.routingRules.copy.celFieldsRouting_image_variation") },
			{ name: "speech", label: i18n.t("workspace.routingRules.copy.celFieldsRouting_speech") },
			{ name: "speech_stream", label: i18n.t("workspace.routingRules.copy.celFieldsRouting_speech_streaming") },
			{ name: "transcription", label: i18n.t("workspace.routingRules.copy.celFieldsRouting_transcription") },
			{ name: "transcription_stream", label: i18n.t("workspace.routingRules.copy.celFieldsRouting_transcription_streaming") },
			{ name: "count_tokens", label: i18n.t("workspace.routingRules.copy.celFieldsRouting_count_tokens") },
			{ name: "rerank", label: i18n.t("workspace.routingRules.copy.celFieldsRouting_rerank") },
			{ name: "video_generation", label: i18n.t("workspace.routingRules.copy.celFieldsRouting_video_generation") },
		],
		description: i18n.t("workspace.routingRules.copy.celFieldsRouting_filter_rules_by_the_type_of_api_request_chat_text_embedd"),
	},
	{
		name: "headers",
		label: i18n.t("workspace.routingRules.copy.celFieldsRouting_header"),
		placeholder: i18n.t("workspace.routingRules.copy.celFieldsRouting_e_g_authorization_x_custom_header_use_lowercase"),
		inputType: "keyValue",
		valueEditorType: "keyValue",
		operators: ["=", "!=", "contains", "beginsWith", "endsWith", "matches", "null", "notNull"],
		defaultOperator: "=",
	},
	{
		name: "tokens_used",
		label: i18n.t("workspace.routingRules.copy.celFieldsRouting_tokens_used"),
		placeholder: "e.g., 80",
		inputType: "text",
		valueEditorType: "number",
		operators: ["=", "!=", ">", "<", ">=", "<="],
		defaultOperator: ">=",
		description: i18n.t("workspace.routingRules.copy.celFieldsRouting_check_token_usage_as_percentage_checked_against_max_of_m"),
	},
	{
		name: "request",
		label: i18n.t("workspace.routingRules.copy.celFieldsRouting_request"),
		placeholder: "e.g., 80",
		inputType: "text",
		valueEditorType: "number",
		operators: ["=", "!=", ">", "<", ">=", "<="],
		defaultOperator: ">=",
		description: i18n.t("workspace.routingRules.copy.celFieldsRouting_check_request_usage_as_percentage_checked_against_max_of"),
	},
	{
		name: "budget_used",
		label: i18n.t("workspace.routingRules.copy.celFieldsRouting_budget_used"),
		placeholder: "e.g., 50",
		inputType: "text",
		valueEditorType: "number",
		operators: ["=", "!=", ">", "<", ">=", "<="],
		defaultOperator: ">=",
		description: i18n.t("workspace.routingRules.copy.celFieldsRouting_check_budget_usage_as_percentage_checked_against_max_of_"),
	},
	{
		name: "complexity_tier",
		label: i18n.t("workspace.routingRules.copy.celFieldsRouting_complexity_tier"),
		placeholder: i18n.t("workspace.routingRules.copy.celFieldsRouting_select_complexity_tier"),
		inputType: "select",
		valueEditorType: "select",
		operators: ["=", "!=", "in", "notIn"],
		defaultOperator: "=",
		values: COMPLEXITY_TIER_VALUES.map((tier) => ({ name: tier, label: tier.charAt(0) + tier.slice(1).toLowerCase() })),
	},
	{
		name: "params",
		label: i18n.t("workspace.routingRules.copy.celFieldsRouting_query_parameter"),
		placeholder: i18n.t("workspace.routingRules.copy.celFieldsRouting_e_g_api_key_user_id"),
		inputType: "keyValue",
		valueEditorType: "keyValue",
		operators: ["=", "!=", "contains", "beginsWith", "endsWith", "matches", "null", "notNull"],
		defaultOperator: "=",
	},
];

/**
 * Get routing fields with dynamic providers and models
 * Provider field values are populated dynamically from available providers
 * Metric options for rate limits and budget are populated from available providers and models
 */
export function getRoutingFields(providers: string[] = [], models: string[] = []): CELFieldDefinition[] {
	// Create provider field values
	const providerValues =
		providers.length > 0
			? providers.map((provider) => ({
					name: provider,
					label: getProviderLabel(provider),
				}))
			: [{ name: "_no_providers", label: i18n.t("workspace.routingRules.copy.celFieldsRouting_no_providers_configured"), disabled: true }];

	// Create model field values
	const modelValues =
		models.length > 0
			? models.map((model) => ({
					name: model,
					label: model,
				}))
			: [];

	// Create metric options for scope input: providers + models
	const scopeOptions = [
		{ name: "", label: i18n.t("workspace.routingRules.copy.celFieldsRouting_provider_level") }, // Empty scope for provider-level
		...providers.map((provider) => ({
			name: provider,
			label: i18n.t("workspace.routingRules.copy.celFieldsRouting_provider_2", { value0: provider }),
		})),
		...models.map((model) => ({
			name: model,
			label: i18n.t("workspace.routingRules.copy.celFieldsRouting_model_2", { value0: model }),
		})),
	];

	// Update provider field with dynamic values and rate limit/budget fields with scope options
	const fieldsWithDynamicValues = baseRoutingFields.map((field) => {
		if (field.name === "provider") {
			return {
				...field,
				values: providerValues,
			};
		}
		if (field.name === "model") {
			return {
				...field,
				values: modelValues,
			};
		}
		if (field.name === "tokens_used" || field.name === "request" || field.name === "budget_used") {
			return {
				...field,
				metricOptions: scopeOptions,
			};
		}
		return field;
	});

	return fieldsWithDynamicValues;
}

export const PROVIDER_DISPLAY_NAMES: Record<string, string> = {
	openai: "OpenAI",
	anthropic: "Anthropic",
	azure: "Azure OpenAI",
	gemini: "Google Gemini",
	vertex: "Vertex AI",
	cohere: "Cohere",
};