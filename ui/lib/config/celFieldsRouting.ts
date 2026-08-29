/**
 * CEL Fields Configuration for Routing Rules
 * Defines available fields for building routing rule expressions
 */

import { getProviderLabel } from "@/lib/constants/logs";
import { localize } from "@/lib/i18n/language";
import { COMPLEXITY_TIER_VALUES } from "@/lib/types/complexityRouter";

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
		label: localize("Model", "模型"),
		placeholder: localize("e.g., gpt-4, claude-3-sonnet", "例如：gpt-4、claude-3-sonnet"),
		inputType: "text",
		valueEditorType: (operator: string) =>
			operator === "=" || operator === "!=" ? "select" : operator === "in" || operator === "notIn" ? "select" : "text",
		operators: ["=", "!=", "in", "notIn", "contains", "beginsWith", "endsWith", "matches"],
		defaultOperator: "=",
	},
	{
		name: "provider",
		label: localize("Provider", "供应商"),
		placeholder: localize("Select provider", "选择供应商"),
		inputType: "select",
		valueEditorType: (operator: string) =>
			operator === "matches" ? "text" : operator === "in" || operator === "notIn" ? "select" : "select",
		operators: ["=", "!=", "in", "notIn", "matches", "null", "notNull"],
		defaultOperator: "=",
	},
	{
		name: "request_type",
		label: localize("Request Type", "请求类型"),
		placeholder: localize("Select request type", "选择请求类型"),
		inputType: "select",
		valueEditorType: (operator: string) =>
			operator === "matches" ? "text" : operator === "in" || operator === "notIn" ? "select" : "select",
		operators: ["=", "!=", "in", "notIn", "matches"],
		defaultOperator: "=",
		values: [
			{ name: "text_completion", label: localize("Text Completion", "文本补全") },
			{ name: "text_completion_stream", label: localize("Text Completion (Streaming)", "文本补全（流式）") },
			{ name: "chat_completion", label: localize("Chat Completion", "对话补全") },
			{ name: "chat_completion_stream", label: localize("Chat Completion (Streaming)", "对话补全（流式）") },
			{ name: "responses", label: "Responses" },
			{ name: "responses_stream", label: localize("Responses (Streaming)", "Responses（流式）") },
			{ name: "embedding", label: localize("Embeddings", "向量嵌入") },
			{ name: "image_generation", label: localize("Image Generation", "图片生成") },
			{ name: "image_generation_stream", label: localize("Image Generation (Streaming)", "图片生成（流式）") },
			{ name: "image_edit", label: localize("Image Edit", "图片编辑") },
			{ name: "image_edit_stream", label: localize("Image Edit (Streaming)", "图片编辑（流式）") },
			{ name: "image_variation", label: localize("Image Variation", "图片变体") },
			{ name: "speech", label: localize("Speech", "语音生成") },
			{ name: "speech_stream", label: localize("Speech (Streaming)", "语音生成（流式）") },
			{ name: "transcription", label: localize("Transcription", "语音转写") },
			{ name: "transcription_stream", label: localize("Transcription (Streaming)", "语音转写（流式）") },
			{ name: "count_tokens", label: localize("Count Tokens", "Token 计数") },
			{ name: "rerank", label: localize("Rerank", "重排序") },
			{ name: "video_generation", label: localize("Video Generation", "视频生成") },
		],
		description: localize(
			"Filter rules by the type of API request (chat, text, embeddings, images, audio, etc.). Streaming and non-streaming requests are distinct types: select both to cover all requests of a kind.",
			"按 API 请求类型筛选规则（对话、文本、向量、图片、音频等）。流式和非流式请求是不同类型；如需覆盖同类全部请求，请同时选择两者。",
		),
	},
	{
		name: "headers",
		label: localize("Header", "请求头"),
		placeholder: localize("e.g., authorization, x-custom-header (use lowercase)", "例如：authorization、x-custom-header（使用小写）"),
		inputType: "keyValue",
		valueEditorType: "keyValue",
		operators: ["=", "!=", "contains", "beginsWith", "endsWith", "matches", "null", "notNull"],
		defaultOperator: "=",
	},
	{
		name: "tokens_used",
		label: localize("Tokens Used (%)", "Token 使用率（%）"),
		placeholder: "e.g., 80",
		inputType: "text",
		valueEditorType: "number",
		operators: ["=", "!=", ">", "<", ">=", "<="],
		defaultOperator: ">=",
		description: localize("Check token usage as percentage. Checked against max of model and provider configs.", "按百分比检查 Token 使用量，并与模型和供应商配置中的最大值比较。"),
	},
	{
		name: "request",
		label: localize("Request (%)", "请求使用率（%）"),
		placeholder: "e.g., 80",
		inputType: "text",
		valueEditorType: "number",
		operators: ["=", "!=", ">", "<", ">=", "<="],
		defaultOperator: ">=",
		description: localize("Check request usage as percentage. Checked against max of model and provider configs.", "按百分比检查请求使用量，并与模型和供应商配置中的最大值比较。"),
	},
	{
		name: "budget_used",
		label: localize("Budget Used (%)", "预算使用率（%）"),
		placeholder: "e.g., 50",
		inputType: "text",
		valueEditorType: "number",
		operators: ["=", "!=", ">", "<", ">=", "<="],
		defaultOperator: ">=",
		description: localize("Check budget usage as percentage. Checked against max of model and provider configs.", "按百分比检查预算使用量，并与模型和供应商配置中的最大值比较。"),
	},
	{
		name: "complexity_tier",
		label: localize("Complexity Tier", "复杂度等级"),
		placeholder: localize("Select complexity tier", "选择复杂度等级"),
		inputType: "select",
		valueEditorType: "select",
		operators: ["=", "!=", "in", "notIn"],
		defaultOperator: "=",
		values: COMPLEXITY_TIER_VALUES.map((tier) => ({ name: tier, label: tier.charAt(0) + tier.slice(1).toLowerCase() })),
	},
	{
		name: "params",
		label: localize("Query Parameter", "请求参数"),
		placeholder: localize("e.g., api_key, user_id", "例如：api_key、user_id"),
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
			: [{ name: "_no_providers", label: localize("No providers configured", "未配置供应商"), disabled: true }];

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
		{ name: "", label: localize("(provider-level)", "（供应商级）") }, // Empty scope for provider-level
		...providers.map((provider) => ({
			name: provider,
			label: localize(`${provider} (provider)`, `${provider}（供应商）`),
		})),
		...models.map((model) => ({
			name: model,
			label: localize(`${model} (model)`, `${model}（模型）`),
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
