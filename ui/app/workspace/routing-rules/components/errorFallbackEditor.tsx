import { Button } from "@/components/ui/button";
import { ComboboxSelect } from "@/components/ui/combobox";
import { Label } from "@/components/ui/label";
import { ModelMultiselect } from "@/components/ui/modelMultiselect";
import { Switch } from "@/components/ui/switch";
import { createDefaultRoutingErrorFallback, RoutingErrorFallbackFormData } from "@/lib/types/routingRules";
import { ArrowDown, ArrowUp, Plus, ShieldCheck, Trash2 } from "lucide-react";
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
		fallbacks: [""],
	};
}

export function ErrorFallbackEditor({ value, providerOptions, onChange }: ErrorFallbackEditorProps) {
	const rule = value.find((item) => item.mode === "scenario" && item.scenario === "content_policy");
	const enabled = Boolean(rule);

	const setEnabled = (nextEnabled: boolean) => {
		onChange(nextEnabled ? [rule ?? createContentSafetyRule()] : []);
	};

	const updateFallbacks = (fallbacks: string[]) => {
		onChange([{ ...(rule ?? createContentSafetyRule()), fallbacks }]);
	};

	return (
		<section className="space-y-4" data-testid="routing-rule-error-fallbacks-section">
			<div className="flex items-start justify-between gap-4 rounded-sm border p-4">
				<div className="flex min-w-0 gap-3">
					<div className="bg-muted flex size-9 shrink-0 items-center justify-center rounded-sm border">
						<ShieldCheck className="size-4" />
					</div>
					<div>
						<Label>{i18n.t("workspace.routingRules.copy.errorFallbackEditor_use_a_dedicated_fallback_for_content_safety_blocks")}</Label>
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

			{enabled && rule ? (
				<div className="space-y-4 rounded-sm border p-4" data-testid="routing-rule-content-safety-fallback-editor">
					<FallbackTargets fallbacks={rule.fallbacks} providerOptions={providerOptions} onChange={updateFallbacks} />
					<div className="bg-muted/30 rounded-sm border px-3 py-2 text-xs">
						<p className="font-medium">{i18n.t("workspace.routingRules.copy.errorFallbackEditor_failure_behavior")}</p>
						<p className="text-muted-foreground mt-1">
							{i18n.t("workspace.routingRules.copy.errorFallbackEditor_if_every_content_safety_fallback_fails_bifrost_returns_t")}
						</p>
					</div>
				</div>
			) : null}
		</section>
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