import { Button } from "@/components/ui/button";
import { ComboboxSelect } from "@/components/ui/combobox";
import { Label } from "@/components/ui/label";
import { ModelMultiselect } from "@/components/ui/modelMultiselect";
import { Switch } from "@/components/ui/switch";
import { localize } from "@/lib/i18n/language";
import { createDefaultRoutingErrorFallback, RoutingErrorFallbackFormData } from "@/lib/types/routingRules";
import { ArrowDown, ArrowUp, Plus, ShieldCheck, Trash2 } from "lucide-react";
import type { ReactNode } from "react";

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
						<Label>{localize("Use a dedicated fallback for content-safety blocks", "内容安全拦截使用专用备用链")}</Label>
						<p className="text-muted-foreground mt-1 max-w-2xl text-xs leading-relaxed">
							{localize(
								"When the provider blocks a request or response for content safety, use this dedicated chain. Rate limits, timeouts, network failures, and all other errors continue through the ordinary fallback chain above.",
								"仅当供应商因内容安全拦截请求或响应时使用这条专用链。限流、超时、网络异常及其他错误仍继续使用上方的普通 Fallback。",
							)}
						</p>
					</div>
				</div>
				<Switch
					checked={enabled}
					onCheckedChange={setEnabled}
					data-testid="routing-rule-content-safety-fallback-toggle"
					aria-label={localize("Enable content-safety fallback", "启用内容安全备用链")}
				/>
			</div>

			{enabled && rule ? (
				<div className="space-y-4 rounded-sm border p-4" data-testid="routing-rule-content-safety-fallback-editor">
					<FallbackTargets fallbacks={rule.fallbacks} providerOptions={providerOptions} onChange={updateFallbacks} />
					<div className="bg-muted/30 rounded-sm border px-3 py-2 text-xs">
						<p className="font-medium">{localize("Failure behavior", "失败后的行为")}</p>
						<p className="text-muted-foreground mt-1">
							{localize(
								"If every content-safety fallback fails, Bifrost returns the original safety error. It does not continue the ordinary chain after entering this dedicated chain.",
								"如果内容安全备用链全部失败，Bifrost 返回原始安全错误；进入专用链后不会再继续普通 Fallback。",
							)}
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
					<Label>{localize("Content-safety fallback targets", "内容安全备用目标")}</Label>
					<p className="text-muted-foreground mt-1 text-xs">{localize("Tried in order until one succeeds.", "按顺序尝试，成功后停止。")}</p>
				</div>
				<Button
					type="button"
					variant="outline"
					size="sm"
					onClick={() => onChange([...fallbacks, ""])}
					data-testid="routing-rule-content-safety-add-target"
				>
					<Plus className="size-4" /> {localize("Add target", "添加目标")}
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
								placeholder={localize("Select provider...", "选择供应商...")}
								className="h-9"
								noPortal
							/>
						</div>
						<div className="min-w-0 flex-1">
							<ModelMultiselect
								provider={provider || undefined}
								value={model}
								onChange={(nextModel) => updateFallback(index, provider, nextModel)}
								placeholder={localize("Incoming model (optional)", "传入模型（可选）")}
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
							<ArrowUp className="size-4" />
						</Button>
						<Button
							type="button"
							variant="ghost"
							size="icon"
							onClick={() => moveFallback(index, 1)}
							disabled={index === fallbacks.length - 1}
							aria-label={localize("Move target down", "下移目标")}
						>
							<ArrowDown className="size-4" />
						</Button>
						<Button
							type="button"
							variant="ghost"
							size="icon"
							onClick={() => onChange(fallbacks.filter((_, current) => current !== index))}
							aria-label={localize("Remove target", "删除目标")}
						>
							<Trash2 className="size-4" />
						</Button>
					</div>
				);
			})}
		</div>
	);
}