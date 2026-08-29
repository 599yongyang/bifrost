import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { ComboboxSelect } from "@/components/ui/combobox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ModelMultiselect } from "@/components/ui/modelMultiselect";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { localize } from "@/lib/i18n/language";
import { createDefaultRoutingErrorFallback, RoutingErrorFallbackCategory, RoutingErrorFallbackFormData } from "@/lib/types/routingRules";
import { switchErrorFallbackMode } from "@/lib/utils/errorFallbackRules";
import { cn } from "@/lib/utils";
import { ArrowDown, ArrowUp, ChevronDown, Plus, Trash2 } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";

const ERROR_CATEGORIES: RoutingErrorFallbackCategory[] = [
	"content_policy",
	"unsupported_operation",
	"rate_limit",
	"authentication",
	"billing",
	"permission",
	"timeout",
	"provider_unavailable",
	"network",
	"invalid_request",
	"internal",
	"unknown",
];

const categoryLabel = (category: RoutingErrorFallbackCategory): string => {
	const labels: Record<RoutingErrorFallbackCategory, [string, string]> = {
		content_policy: ["Content safety block", "内容安全拦截"],
		unsupported_operation: ["Unsupported operation", "不支持此操作"],
		rate_limit: ["Too many requests", "请求过多"],
		authentication: ["Authentication failed", "身份验证失败"],
		billing: ["Billing or balance issue", "余额或计费问题"],
		permission: ["Permission denied", "权限不足"],
		timeout: ["Request timeout", "请求超时"],
		provider_unavailable: ["Provider temporarily unavailable", "供应商暂时不可用"],
		network: ["Network error", "网络错误"],
		invalid_request: ["Invalid request", "请求参数错误"],
		internal: ["Bifrost internal error", "Bifrost 内部错误"],
		unknown: ["Unknown error", "未知错误"],
	};
	return localize(...labels[category]);
};

const splitStrings = (value: string): string[] =>
	value
		.split(",")
		.map((item) => item.trim())
		.filter(Boolean);

const splitNumbers = (value: string): number[] => splitStrings(value).map(Number).filter(Number.isFinite);

const splitFallback = (fallback: string): [string, string] => {
	const [provider = "", ...model] = fallback.split("/");
	return [provider, model.join("/")];
};

interface ErrorFallbackEditorProps {
	value: RoutingErrorFallbackFormData[];
	providerOptions: Array<{ label: string; value: string; icon: ReactNode }>;
	onChange: (value: RoutingErrorFallbackFormData[]) => void;
}

export function ErrorFallbackEditor({ value, providerOptions, onChange }: ErrorFallbackEditorProps) {
	const [expandedIndex, setExpandedIndex] = useState<number | null>(null);
	const updateRule = (index: number, update: (rule: RoutingErrorFallbackFormData) => RoutingErrorFallbackFormData) => {
		onChange(value.map((rule, currentIndex) => (currentIndex === index ? update(rule) : rule)));
	};

	const addRule = () => {
		const next = createDefaultRoutingErrorFallback();
		next.fallbacks = [""];
		onChange([...value, next]);
		setExpandedIndex(value.length);
	};

	return (
		<div className="space-y-3" data-testid="routing-rule-error-fallbacks-section">
			<div className="flex items-start justify-between gap-3">
				<div>
					<Label>{localize("Use alternatives on errors", "出错时改用")}</Label>
					<p className="text-muted-foreground mt-1 text-xs">
						{localize(
							"Use a dedicated fallback chain for safety blocks, rate limits, timeouts and other recognized errors.",
							"为安全拦截、限流、超时和其他已识别错误配置专用备用链。",
						)}
					</p>
				</div>
				<Button type="button" variant="outline" size="sm" onClick={addRule} data-testid="routing-rule-add-error-fallback-button">
					<Plus className="mr-1 h-4 w-4" /> {localize("Add error handling", "添加出错处理")}
				</Button>
			</div>

			{value.length === 0 ? (
				<p className="text-muted-foreground text-sm">{localize("No error-aware fallbacks configured", "未配置错误专用备用链")}</p>
			) : (
				value.map((rule, ruleIndex) => (
					<ErrorFallbackRuleCard
						key={ruleIndex}
						rule={rule}
						ruleIndex={ruleIndex}
						open={expandedIndex === ruleIndex}
						onOpenChange={(open) => setExpandedIndex(open ? ruleIndex : null)}
						providerOptions={providerOptions}
						updateRule={updateRule}
						onRemove={() => {
							onChange(value.filter((_, index) => index !== ruleIndex));
							setExpandedIndex(null);
						}}
					/>
				))
			)}

			<p className="text-muted-foreground text-xs">
				{localize(
					"If every dedicated fallback fails, Bifrost returns the original error and does not continue the ordinary fallback chain.",
					"专用链全部失败后，将返回原始错误，不会继续普通备用链。",
				)}
			</p>
		</div>
	);
}

function ErrorFallbackRuleCard({
	rule,
	ruleIndex,
	open,
	onOpenChange,
	providerOptions,
	updateRule,
	onRemove,
}: {
	rule: RoutingErrorFallbackFormData;
	ruleIndex: number;
	open: boolean;
	onOpenChange: (open: boolean) => void;
	providerOptions: Array<{ label: string; value: string; icon: ReactNode }>;
	updateRule: (index: number, update: (rule: RoutingErrorFallbackFormData) => RoutingErrorFallbackFormData) => void;
	onRemove: () => void;
}) {
	const title = rule.name.trim() || localize(`Error handling ${ruleIndex + 1}`, `出错处理 ${ruleIndex + 1}`);
	const summary = rule.mode === "scenario" ? categoryLabel(rule.scenario) : localize("Expert matcher", "专家匹配");
	const configuredTargets = rule.fallbacks.filter(Boolean).length;

	return (
		<Collapsible
			open={open}
			onOpenChange={onOpenChange}
			className="bg-card overflow-hidden rounded-lg border"
			data-testid={`routing-rule-error-fallback-${ruleIndex}`}
		>
			<div className="flex items-center gap-3 px-4 py-3">
				<CollapsibleTrigger asChild>
					<button type="button" className="min-w-0 flex-1 text-left">
						<div className="flex items-center gap-2">
							<span className="truncate text-sm font-semibold">{title}</span>
							<Badge variant="outline">{summary}</Badge>
						</div>
						<p className="text-muted-foreground mt-1 text-xs">
							{configuredTargets > 0
								? localize(`${configuredTargets} fallback target${configuredTargets === 1 ? "" : "s"}`, `${configuredTargets} 个备用目标`)
								: localize("No fallback target selected", "尚未选择备用目标")}
						</p>
					</button>
				</CollapsibleTrigger>
				<Button type="button" variant="ghost" size="icon" onClick={onRemove} aria-label={localize("Remove rule", "删除规则")}>
					<Trash2 className="size-4" />
				</Button>
				<CollapsibleTrigger asChild>
					<Button type="button" variant="ghost" size="icon" aria-label={localize("Edit rule", "编辑规则")}>
						<ChevronDown className={cn("size-4 transition-transform duration-150", open && "rotate-180")} />
					</Button>
				</CollapsibleTrigger>
			</div>

			<CollapsibleContent className="space-y-4 border-t p-4">
				<div className="flex items-end justify-between gap-3">
					<div className="min-w-0 flex-1 space-y-2">
						<Label>{localize("Rule note (optional)", "规则备注（可选）")}</Label>
						<Input
							value={rule.name}
							onChange={(event) => updateRule(ruleIndex, (current) => ({ ...current, name: event.target.value }))}
							placeholder={localize("For example: Image safety block", "例如：图片安全拦截")}
						/>
					</div>
					<Button
						type="button"
						variant="outline"
						size="sm"
						onClick={() =>
							updateRule(ruleIndex, (current) => switchErrorFallbackMode(current, current.mode === "scenario" ? "legacy" : "scenario"))
						}
					>
						{rule.mode === "scenario" ? localize("Use expert matcher", "使用专家匹配") : localize("Use scenario matcher", "使用场景匹配")}
					</Button>
				</div>

				{rule.mode === "scenario" ? (
					<ScenarioMatcher rule={rule} ruleIndex={ruleIndex} updateRule={updateRule} />
				) : (
					<ExpertMatcher rule={rule} ruleIndex={ruleIndex} updateRule={updateRule} />
				)}

				<FallbackTargets
					fallbacks={rule.fallbacks}
					providerOptions={providerOptions}
					onChange={(fallbacks) => updateRule(ruleIndex, (current) => ({ ...current, fallbacks }))}
				/>
			</CollapsibleContent>
		</Collapsible>
	);
}

interface MatcherProps {
	rule: RoutingErrorFallbackFormData;
	ruleIndex: number;
	updateRule: (index: number, update: (rule: RoutingErrorFallbackFormData) => RoutingErrorFallbackFormData) => void;
}

function ScenarioMatcher({ rule, ruleIndex, updateRule }: MatcherProps) {
	const updateSupplement = (field: keyof RoutingErrorFallbackFormData["supplement"], values: string[] | number[]) =>
		updateRule(ruleIndex, (current) => ({ ...current, supplement: { ...current.supplement, [field]: values } }));

	return (
		<div className="bg-muted/30 space-y-3 rounded-md p-3">
			<div className="space-y-2">
				<Label>{localize("Error scenario", "错误场景")}</Label>
				<Select
					value={rule.scenario}
					onValueChange={(scenario) =>
						updateRule(ruleIndex, (current) => ({ ...current, scenario: scenario as RoutingErrorFallbackCategory }))
					}
				>
					<SelectTrigger data-testid={`routing-rule-error-fallback-${ruleIndex}-scenario`}>
						<SelectValue />
					</SelectTrigger>
					<SelectContent>
						{ERROR_CATEGORIES.map((category) => (
							<SelectItem key={category} value={category}>
								{categoryLabel(category)}
							</SelectItem>
						))}
					</SelectContent>
				</Select>
			</div>
			<p className="text-muted-foreground text-xs">
				{localize(
					"Bifrost recognizes common provider and multilingual errors. Add supplemental clues only when needed.",
					"Bifrost 已识别常见供应商及多语言错误，仅在需要时补充识别线索。",
				)}
			</p>
			<div className="grid grid-cols-1 gap-3 md:grid-cols-2">
				<CommaInput
					label={localize("Limit to providers", "仅限供应商")}
					value={rule.supplement.providers}
					onChange={(values) => updateSupplement("providers", values)}
					placeholder="azure, openai"
				/>
				<CommaInput
					label={localize("Message contains any", "消息包含任一线索")}
					value={rule.supplement.message_contains_any}
					onChange={(values) => updateSupplement("message_contains_any", values)}
					placeholder="unsafe, content filtered"
				/>
				<CommaInput
					label={localize("Error types", "错误类型")}
					value={rule.supplement.error_types}
					onChange={(values) => updateSupplement("error_types", values)}
				/>
				<CommaInput
					label={localize("Error codes", "错误代码")}
					value={rule.supplement.error_codes}
					onChange={(values) => updateSupplement("error_codes", values)}
				/>
				<NumberListInput
					label={localize("Status codes", "状态码")}
					value={rule.supplement.status_codes}
					onChange={(values) => updateSupplement("status_codes", values)}
				/>
			</div>
		</div>
	);
}

function ExpertMatcher({ rule, ruleIndex, updateRule }: MatcherProps) {
	const updateWhen = (field: keyof RoutingErrorFallbackFormData["when"], values: string[] | number[]) =>
		updateRule(ruleIndex, (current) => ({ ...current, when: { ...current.when, [field]: values } }));

	return (
		<div className="space-y-3 rounded-md border border-dashed p-3">
			<p className="text-muted-foreground text-xs">
				{localize(
					"All populated fields must match. Untouched legacy rules preserve their original payload.",
					"所有已填写字段必须同时匹配；未修改的旧规则会保留原始数据。",
				)}
			</p>
			<div className="grid grid-cols-1 gap-3 md:grid-cols-2">
				<CommaInput
					label={localize("Categories", "错误类别")}
					value={rule.when.categories}
					onChange={(values) =>
						updateWhen(
							"categories",
							values.filter((value) => ERROR_CATEGORIES.includes(value as RoutingErrorFallbackCategory)),
						)
					}
					placeholder="timeout, rate_limit"
				/>
				<CommaInput
					label={localize("Error types", "错误类型")}
					value={rule.when.error_types}
					onChange={(values) => updateWhen("error_types", values)}
				/>
				<CommaInput
					label={localize("Error codes", "错误代码")}
					value={rule.when.error_codes}
					onChange={(values) => updateWhen("error_codes", values)}
				/>
				<NumberListInput
					label={localize("Status codes", "状态码")}
					value={rule.when.status_codes}
					onChange={(values) => updateWhen("status_codes", values)}
				/>
				<CommaInput
					label={localize("Message contains", "消息包含")}
					value={rule.when.message_contains}
					onChange={(values) => updateWhen("message_contains", values)}
				/>
			</div>
		</div>
	);
}

function CommaInput({
	label,
	value,
	onChange,
	placeholder,
}: {
	label: string;
	value: string[];
	onChange: (value: string[]) => void;
	placeholder?: string;
}) {
	const serialized = value.join(", ");
	const [text, setText] = useState(serialized);
	useEffect(() => setText(serialized), [serialized]);
	const commit = () => onChange(splitStrings(text));

	return (
		<div className="space-y-2">
			<Label>{label}</Label>
			<Input
				value={text}
				onChange={(event) => setText(event.target.value)}
				onBlur={commit}
				onKeyDown={(event) => {
					if (event.key === "Enter") {
						event.preventDefault();
						commit();
					}
				}}
				placeholder={placeholder || localize("comma separated", "逗号分隔")}
			/>
		</div>
	);
}

function NumberListInput({ label, value, onChange }: { label: string; value: number[]; onChange: (value: number[]) => void }) {
	const serialized = value.join(", ");
	const [text, setText] = useState(serialized);
	useEffect(() => setText(serialized), [serialized]);
	const commit = () => onChange(splitNumbers(text));

	return (
		<div className="space-y-2">
			<Label>{label}</Label>
			<Input
				value={text}
				onChange={(event) => setText(event.target.value)}
				onBlur={commit}
				onKeyDown={(event) => {
					if (event.key === "Enter") {
						event.preventDefault();
						commit();
					}
				}}
				placeholder="400, 429, 503"
			/>
		</div>
	);
}

function FallbackTargets({
	fallbacks,
	providerOptions,
	onChange,
}: {
	fallbacks: string[];
	providerOptions: Array<{ label: string; value: string; icon: ReactNode }>;
	onChange: (fallbacks: string[]) => void;
}) {
	const updateFallback = (index: number, provider: string, model: string) => {
		const next = [...fallbacks];
		next[index] = `${provider}/${model}`;
		onChange(next);
	};
	const moveFallback = (index: number, delta: number) => {
		const target = index + delta;
		if (target < 0 || target >= fallbacks.length) return;
		const next = [...fallbacks];
		[next[index], next[target]] = [next[target], next[index]];
		onChange(next);
	};

	return (
		<div className="space-y-3">
			<div className="flex items-center justify-between">
				<div>
					<Label>{localize("Fallback targets", "备用目标")}</Label>
					<p className="text-muted-foreground mt-1 text-xs">{localize("Tried in order until one succeeds.", "按顺序尝试，成功后停止。")}</p>
				</div>
				<Button type="button" variant="outline" size="sm" onClick={() => onChange([...fallbacks, ""])}>
					<Plus className="mr-1 h-4 w-4" /> {localize("Add target", "添加目标")}
				</Button>
			</div>
			{fallbacks.map((fallback, index) => {
				const [provider, model] = splitFallback(fallback);
				return (
					<div key={index} className="flex items-center gap-2" data-testid={`routing-rule-error-fallback-target-${index}`}>
						<div className="flex-1">
							<ComboboxSelect
								value={provider}
								onValueChange={(nextProvider) => {
									const selectedProvider = nextProvider ?? "";
									updateFallback(index, selectedProvider, selectedProvider === provider ? model : "");
								}}
								options={providerOptions}
								placeholder={localize("Select provider...", "选择提供商...")}
								className="h-9"
								noPortal
							/>
						</div>
						<div className="flex-1">
							<ModelMultiselect
								provider={provider || undefined}
								value={model}
								onChange={(nextModel) => updateFallback(index, provider, nextModel)}
								placeholder={localize("Incoming (optional)", "传入（可选）")}
								isSingleSelect
								disabled={!provider}
								className="!h-9 !min-h-9 w-full"
							/>
						</div>
						<Button
							type="button"
							variant="ghost"
							size="icon"
							onClick={() => moveFallback(index, -1)}
							disabled={index === 0}
							aria-label={localize("Move target up", "上移目标")}
						>
							<ArrowUp className="h-4 w-4" />
						</Button>
						<Button
							type="button"
							variant="ghost"
							size="icon"
							onClick={() => moveFallback(index, 1)}
							disabled={index === fallbacks.length - 1}
							aria-label={localize("Move target down", "下移目标")}
						>
							<ArrowDown className="h-4 w-4" />
						</Button>
						<Button
							type="button"
							variant="ghost"
							size="icon"
							onClick={() => onChange(fallbacks.filter((_, current) => current !== index))}
							aria-label={localize("Remove target", "删除目标")}
						>
							<Trash2 className="h-4 w-4" />
						</Button>
					</div>
				);
			})}
		</div>
	);
}