import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { ComboboxSelect } from "@/components/ui/combobox";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Label } from "@/components/ui/label";
import { ModelMultiselect } from "@/components/ui/modelMultiselect";
import { Switch } from "@/components/ui/switch";
import { TagInput } from "@/components/ui/tagInput";
import { createDefaultRoutingErrorFallback, RoutingErrorFallbackFormData } from "@/lib/types/routingRules";
import { hasContentSafetyRecognitionDraft } from "@/lib/utils/errorFallbackRules";
import { useGetContentSafetySignalsQuery } from "@/lib/store/apis/routingRulesApi";
import { ArrowDown, ArrowUp, ChevronDown, Plus, ScanSearch, ShieldCheck, Trash2 } from "lucide-react";
import type { ReactNode } from "react";
import i18n from "@/lib/i18n";

const splitFallback = (fallback: string): [string, string] => {
	const [provider = "", ...model] = fallback.split("/");
	return [provider, model.join("/")];
};

interface ErrorFallbackEditorProps {
	value: RoutingErrorFallbackFormData[];
	providerOptions: Array<{ label: string; value: string; icon: ReactNode }>;
	onChange: (value: RoutingErrorFallbackFormData[]) => void;
}

function createContentSafetyRule(): RoutingErrorFallbackFormData {
	const rule = createDefaultRoutingErrorFallback();
	return {
		...rule,
		mode: "scenario",
		name: "content-safety",
		scenario: "content_policy",
		fallbacks: [],
	};
}

function shouldRetainContentSafetyForm(rule: RoutingErrorFallbackFormData): boolean {
	return Boolean(rule.originalContentSafetyRule) || hasContentSafetyRecognitionDraft(rule);
}

export function ErrorFallbackEditor({ value, providerOptions, onChange }: ErrorFallbackEditorProps) {
	const { data: builtInSignals, isLoading: signalsLoading, isError: signalsError } = useGetContentSafetySignalsQuery();
	const structuredSignals = [...new Set([...(builtInSignals?.structured || []), ...(builtInSignals?.finish_reasons || [])])];
	const rule = value.find((item) => item.mode === "scenario" && item.scenario === "content_policy");
	const current = rule ?? createContentSafetyRule();
	const isLegacyRule = Boolean(current.originalContentSafetyRule?.when && !current.originalContentSafetyRule.scenario);
	const enabled = current.fallbacks.length > 0;
	const builtInSignalsStatus = signalsLoading
		? i18n.t("workspace.routingRules.copy.errorFallbackEditor_built_in_rules_loading")
		: signalsError
			? i18n.t("workspace.routingRules.copy.errorFallbackEditor_built_in_rules_unavailable")
			: i18n.t("workspace.routingRules.copy.errorFallbackEditor_built_in_rules_description", {
					codes: structuredSignals.length,
					phrases: builtInSignals?.messages.length || 0,
				});
	const providerScopeHint = isLegacyRule
		? i18n.t("workspace.routingRules.copy.errorFallbackEditor_legacy_provider_scope_hint")
		: i18n.t("workspace.routingRules.copy.errorFallbackEditor_match_providers_hint");

	const setEnabled = (nextEnabled: boolean) => {
		if (nextEnabled) {
			onChange([
				{
					...current,
					fallbacks: current.fallbacks.length > 0 ? current.fallbacks : [""],
				},
			]);
			return;
		}
		const recognitionOnly = { ...current, fallbacks: [] };
		onChange(shouldRetainContentSafetyForm(recognitionOnly) ? [recognitionOnly] : []);
	};

	const updateFallbacks = (fallbacks: string[]) => {
		const next = { ...current, fallbacks };
		onChange(fallbacks.length > 0 || shouldRetainContentSafetyForm(next) ? [next] : []);
	};
	const updateRecognition = (patch: Partial<RoutingErrorFallbackFormData["supplement"]>) => {
		const next = {
			...current,
			supplement: { ...current.supplement, ...patch },
		};
		onChange(enabled || shouldRetainContentSafetyForm(next) ? [next] : []);
	};

	return (
		<section className="space-y-4" data-testid="routing-rule-error-fallbacks-section">
			<div className="overflow-hidden rounded-sm border" data-testid="routing-rule-content-safety-recognition-editor">
				<div className="flex items-start gap-3 p-4">
					<div className="bg-muted flex size-9 shrink-0 items-center justify-center rounded-sm border">
						<ScanSearch className="size-4" />
					</div>
					<div className="min-w-0 flex-1">
						<div className="flex flex-wrap items-center gap-2">
							<Label>{i18n.t("workspace.routingRules.copy.errorFallbackEditor_content_safety_recognition")}</Label>
							<Badge variant="secondary">{i18n.t("workspace.routingRules.copy.errorFallbackEditor_always_enabled")}</Badge>
						</div>
						<p className="text-muted-foreground mt-1 max-w-2xl text-xs leading-relaxed">
							{i18n.t("workspace.routingRules.copy.errorFallbackEditor_recognition_description")}
						</p>
					</div>
				</div>
				<div className="space-y-4 border-t p-4">
					<Collapsible defaultOpen className="group overflow-hidden rounded-sm border">
						<CollapsibleTrigger
							type="button"
							data-testid="routing-rule-content-safety-built-in-signals-toggle"
							className="hover:bg-muted/40 flex w-full items-center justify-between gap-3 px-3 py-2.5 text-left transition-colors"
						>
							<div>
								<p className="text-sm font-medium">{i18n.t("workspace.routingRules.copy.errorFallbackEditor_built_in_rules")}</p>
								<p className="text-muted-foreground mt-0.5 text-xs" aria-live="polite">
									{builtInSignalsStatus}
								</p>
							</div>
							<ChevronDown className="text-muted-foreground size-4 shrink-0 transition-transform group-data-[state=open]:rotate-180" />
						</CollapsibleTrigger>
						<CollapsibleContent className="space-y-3 border-t p-3">
							{signalsLoading || signalsError ? (
								<p className="text-muted-foreground text-xs" role={signalsError ? "alert" : "status"}>
									{builtInSignalsStatus}
								</p>
							) : (
								<>
									<SignalBadges
										label={i18n.t("workspace.routingRules.copy.errorFallbackEditor_error_codes_and_types")}
										values={structuredSignals}
									/>
									<SignalBadges
										label={i18n.t("workspace.routingRules.copy.errorFallbackEditor_message_phrases")}
										values={builtInSignals?.messages || []}
									/>
								</>
							)}
						</CollapsibleContent>
					</Collapsible>

					<div className="space-y-4">
						<div>
							<Label>{i18n.t("workspace.routingRules.copy.errorFallbackEditor_custom_recognition_clues")}</Label>
							{isLegacyRule ? (
								<Badge variant="outline" className="ml-2">
									{i18n.t("workspace.routingRules.copy.errorFallbackEditor_legacy_and_matching")}
								</Badge>
							) : null}
							<p className="text-muted-foreground mt-1 text-xs leading-relaxed">
								{i18n.t("workspace.routingRules.copy.errorFallbackEditor_custom_recognition_clues_description")}
							</p>
						</div>
						<div className="grid gap-4 lg:grid-cols-2">
							<div className="space-y-2">
								<Label>{i18n.t("workspace.routingRules.copy.errorFallbackEditor_match_providers")}</Label>
								<ComboboxSelect
									multiple
									disabled={isLegacyRule}
									value={current.supplement.providers}
									onValueChange={(providers) => updateRecognition({ providers })}
									options={providerOptions}
									placeholder={i18n.t("workspace.routingRules.copy.errorFallbackEditor_all_providers")}
									searchPlaceholder={i18n.t("workspace.routingRules.copy.errorFallbackEditor_search_providers")}
									data-testid="routing-rule-content-safety-match-providers"
									noPortal
								/>
								<p className="text-muted-foreground text-xs">{providerScopeHint}</p>
							</div>
							<div className="space-y-2">
								<Label>{i18n.t("workspace.routingRules.copy.errorFallbackEditor_error_message_keywords")}</Label>
								<TagInput
									value={current.supplement.message_contains_any}
									onValueChange={(message_contains_any) => updateRecognition({ message_contains_any })}
									placeholder={i18n.t("workspace.routingRules.copy.errorFallbackEditor_error_message_keywords_placeholder")}
									data-testid="routing-rule-content-safety-message-keywords"
								/>
								<p className="text-muted-foreground text-xs">
									{i18n.t("workspace.routingRules.copy.errorFallbackEditor_error_message_keywords_hint")}
								</p>
							</div>
						</div>
					</div>
				</div>
			</div>

			<div className="overflow-hidden rounded-sm border" data-testid="routing-rule-content-safety-fallback-editor">
				<div className="flex items-start justify-between gap-4 p-4">
					<div className="flex min-w-0 gap-3">
						<div className="bg-muted flex size-9 shrink-0 items-center justify-center rounded-sm border">
							<ShieldCheck className="size-4" />
						</div>
						<div>
							<Label>{i18n.t("workspace.routingRules.copy.errorFallbackEditor_content_safety_handling")}</Label>
							<p className="text-muted-foreground mt-1 max-w-2xl text-xs leading-relaxed">
								{i18n.t("workspace.routingRules.copy.errorFallbackEditor_when_the_provider_blocks_a_request_or_response_for_conte")}
							</p>
						</div>
					</div>
					<Switch
						checked={enabled}
						onCheckedChange={setEnabled}
						data-testid="routing-rule-content-safety-fallback-toggle"
						aria-label={i18n.t("workspace.routingRules.copy.errorFallbackEditor_enable_content_safety_fallback")}
					/>
				</div>

				{enabled ? (
					<div className="space-y-4 border-t p-4">
						<FallbackTargets fallbacks={current.fallbacks} providerOptions={providerOptions} onChange={updateFallbacks} />
						<div className="bg-muted/30 rounded-sm border px-3 py-2 text-xs">
							<p className="font-medium">{i18n.t("workspace.routingRules.copy.errorFallbackEditor_failure_behavior")}</p>
							<p className="text-muted-foreground mt-1">
								{i18n.t("workspace.routingRules.copy.errorFallbackEditor_if_every_content_safety_fallback_fails_bifrost_returns_t")}
							</p>
						</div>
					</div>
				) : (
					<div className="text-muted-foreground border-t px-4 py-3 text-xs">
						{i18n.t("workspace.routingRules.copy.errorFallbackEditor_disabled_behavior")}
					</div>
				)}
			</div>
		</section>
	);
}

function SignalBadges({ label, values }: { label: string; values: readonly string[] }) {
	return (
		<div className="space-y-2">
			<p className="text-muted-foreground text-xs font-medium">{label}</p>
			<div className="flex flex-wrap gap-1.5">
				{values.map((value) => (
					<Badge key={value} variant="outline" className="max-w-full text-left font-mono font-normal break-all whitespace-normal">
						{value}
					</Badge>
				))}
			</div>
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
			<div className="flex items-center justify-between gap-3">
				<div>
					<Label>{i18n.t("workspace.routingRules.copy.errorFallbackEditor_content_safety_fallback_targets")}</Label>
					<p className="text-muted-foreground mt-1 text-xs">
						{i18n.t("workspace.routingRules.copy.errorFallbackEditor_tried_in_order_until_one_succeeds")}
					</p>
				</div>
				<Button
					type="button"
					variant="outline"
					size="sm"
					onClick={() => onChange([...fallbacks, ""])}
					data-testid="routing-rule-content-safety-add-target"
				>
					<Plus className="size-4" /> {i18n.t("workspace.routingRules.copy.errorFallbackEditor_add_target")}
				</Button>
			</div>
			{fallbacks.map((fallback, index) => {
				const [provider, model] = splitFallback(fallback);
				return (
					<div
						key={index}
						className="bg-muted/20 flex items-center gap-2 rounded-sm border p-2"
						data-testid={`routing-rule-error-fallback-target-${index}`}
					>
						<div className="min-w-0 flex-1">
							<ComboboxSelect
								value={provider}
								onValueChange={(nextProvider) => {
									const selectedProvider = nextProvider ?? "";
									updateFallback(index, selectedProvider, selectedProvider === provider ? model : "");
								}}
								options={providerOptions}
								placeholder={i18n.t("workspace.routingRules.copy.errorFallbackEditor_select_provider")}
								className="h-9"
								noPortal
							/>
						</div>
						<div className="min-w-0 flex-1">
							<ModelMultiselect
								provider={provider || undefined}
								value={model}
								onChange={(nextModel) => updateFallback(index, provider, nextModel)}
								placeholder={i18n.t("workspace.routingRules.copy.errorFallbackEditor_incoming_model_optional")}
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
							aria-label={i18n.t("workspace.routingRules.copy.errorFallbackEditor_move_target_up")}
						>
							<ArrowUp className="size-4" />
						</Button>
						<Button
							type="button"
							variant="ghost"
							size="icon"
							onClick={() => moveFallback(index, 1)}
							disabled={index === fallbacks.length - 1}
							aria-label={i18n.t("workspace.routingRules.copy.errorFallbackEditor_move_target_down")}
						>
							<ArrowDown className="size-4" />
						</Button>
						<Button
							type="button"
							variant="ghost"
							size="icon"
							onClick={() => onChange(fallbacks.filter((_, current) => current !== index))}
							aria-label={i18n.t("workspace.routingRules.copy.errorFallbackEditor_remove_target")}
						>
							<Trash2 className="size-4" />
						</Button>
					</div>
				);
			})}
		</div>
	);
}