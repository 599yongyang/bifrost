import { Button } from "@/components/ui/button";
import { ComboboxSelect } from "@/components/ui/combobox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ModelMultiselect } from "@/components/ui/modelMultiselect";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { RenderProviderIcon, type ProviderIconType } from "@/lib/constants/icons";
import { getProviderLabel } from "@/lib/constants/logs";
import i18n from "@/lib/i18n";
import { getErrorMessage } from "@/lib/store";
import { useGetAllKeysQuery, useGetProvidersQuery } from "@/lib/store/apis/providersApi";
import {
	DEFAULT_CIRCUIT_BREAKER_POLICY,
	getSignalMatchMode,
	type CircuitBreakerMatchMode,
	type CircuitBreakerOperator,
	type CircuitBreakerPolicy,
	type CircuitBreakerSignal,
} from "@/lib/types/circuitBreaker";
import { AlertCircle, ArrowRight, Plus, Trash2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { toast } from "sonner";

interface CircuitBreakerSheetProps {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	editingPolicy?: CircuitBreakerPolicy | null;
	policies: CircuitBreakerPolicy[];
	onSave: (policy: CircuitBreakerPolicy, originalName?: string) => Promise<void>;
}

const durationPattern = /^(?:[0-9]+(?:\.[0-9]+)?(?:ns|us|µs|ms|s|m|h))+$/;

function clonePolicy(policy?: CircuitBreakerPolicy | null): CircuitBreakerPolicy {
	const source = policy ?? DEFAULT_CIRCUIT_BREAKER_POLICY;
	return {
		...source,
		primary_key_ids: [...(source.primary_key_ids ?? [])],
		condition: {
			operator: source.condition.operator ?? "OR",
			signals: source.condition.signals.map((signal) => ({ ...signal })),
		},
	};
}

function updateSignalMode(signal: CircuitBreakerSignal, mode: CircuitBreakerMatchMode): CircuitBreakerSignal {
	const next: CircuitBreakerSignal = { source: "response_header", header_name: signal.header_name };
	if (mode === "equals") next.header_value = "";
	if (mode === "contains") next.header_contains = "";
	return next;
}

export function CircuitBreakerSheet({ open, onOpenChange, editingPolicy, policies, onSave }: CircuitBreakerSheetProps) {
	const [policy, setPolicy] = useState<CircuitBreakerPolicy>(() => clonePolicy(editingPolicy));
	const [isSaving, setIsSaving] = useState(false);
	const [validationError, setValidationError] = useState<string | null>(null);
	const { data: providers = [] } = useGetProvidersQuery();
	const { data: allKeys = [] } = useGetAllKeysQuery();

	useEffect(() => {
		if (!open) return;
		setPolicy(clonePolicy(editingPolicy));
		setValidationError(null);
	}, [open, editingPolicy]);

	const providerOptions = useMemo(() => {
		const names = new Set(providers.map((provider) => provider.name as string));
		if (policy.primary_provider) names.add(policy.primary_provider);
		if (policy.fallback_provider) names.add(policy.fallback_provider);
		return Array.from(names)
			.sort()
			.map((provider) => ({
				value: provider,
				label: getProviderLabel(provider),
				icon: <RenderProviderIcon provider={provider as ProviderIconType} size="sm" className="size-4" />,
			}));
	}, [providers, policy.primary_provider, policy.fallback_provider]);

	const keyOptions = useMemo(() => {
		const matching = allKeys.filter((key) => key.provider === policy.primary_provider);
		const selected = new Set(policy.primary_key_ids ?? []);
		const options = matching.map((key) => ({ value: key.key_id, label: `${key.name} · ${key.key_id.slice(0, 8)}` }));
		for (const id of selected) {
			if (!options.some((option) => option.value === id)) options.push({ value: id, label: id });
		}
		return options;
	}, [allKeys, policy.primary_provider, policy.primary_key_ids]);

	const updatePolicy = <K extends keyof CircuitBreakerPolicy>(key: K, value: CircuitBreakerPolicy[K]) => {
		setPolicy((current) => ({ ...current, [key]: value }));
		setValidationError(null);
	};

	const updateSignal = (index: number, next: CircuitBreakerSignal) => {
		setPolicy((current) => ({
			...current,
			condition: {
				...current.condition,
				signals: current.condition.signals.map((signal, signalIndex) => (signalIndex === index ? next : signal)),
			},
		}));
		setValidationError(null);
	};

	const addSignal = () => {
		setPolicy((current) => ({
			...current,
			condition: {
				...current.condition,
				signals: [...current.condition.signals, { source: "response_header", header_name: "" }],
			},
		}));
	};

	const removeSignal = (index: number) => {
		setPolicy((current) => ({
			...current,
			condition: {
				...current.condition,
				signals: current.condition.signals.filter((_, signalIndex) => signalIndex !== index),
			},
		}));
	};

	const validate = (): string | null => {
		const name = policy.name.trim();
		if (!name) return i18n.t("workspace.circuitBreaker.validation.name");
		if (policies.some((item) => item.name.toLowerCase() === name.toLowerCase() && item.name !== editingPolicy?.name)) {
			return i18n.t("workspace.circuitBreaker.validation.uniqueName");
		}
		if (!policy.primary_provider || !policy.primary_model.trim()) return i18n.t("workspace.circuitBreaker.validation.primary");
		if (!policy.fallback_provider || !policy.fallback_model.trim()) return i18n.t("workspace.circuitBreaker.validation.fallback");
		if (policy.primary_provider === policy.fallback_provider && policy.primary_model.trim() === policy.fallback_model.trim()) {
			return i18n.t("workspace.circuitBreaker.validation.differentTarget");
		}
		if (policy.condition.signals.length === 0) return i18n.t("workspace.circuitBreaker.validation.signal");
		for (const signal of policy.condition.signals) {
			if (!signal.header_name.trim()) return i18n.t("workspace.circuitBreaker.validation.headerName");
			if (signal.header_value !== undefined && !signal.header_value.trim())
				return i18n.t("workspace.circuitBreaker.validation.headerValue");
			if (signal.header_contains !== undefined && !signal.header_contains.trim())
				return i18n.t("workspace.circuitBreaker.validation.headerContains");
		}
		if (!policy.default_cooldown?.trim() || !durationPattern.test(policy.default_cooldown.trim())) {
			return i18n.t("workspace.circuitBreaker.validation.cooldown");
		}
		return null;
	};

	const handleSave = async () => {
		const error = validate();
		if (error) {
			setValidationError(error);
			return;
		}
		const normalized: CircuitBreakerPolicy = {
			...policy,
			name: policy.name.trim(),
			primary_model: policy.primary_model.trim(),
			fallback_model: policy.fallback_model.trim(),
			primary_key_ids: policy.primary_key_ids?.length ? policy.primary_key_ids : undefined,
			default_cooldown: policy.default_cooldown?.trim() || "30s",
			cooldown_header: policy.cooldown_header?.trim() || undefined,
			condition: {
				operator: policy.condition.operator ?? "OR",
				signals: policy.condition.signals.map((signal) => ({
					...signal,
					header_name: signal.header_name.trim(),
					header_value: signal.header_value?.trim(),
					header_contains: signal.header_contains?.trim(),
				})),
			},
		};
		setIsSaving(true);
		try {
			await onSave(normalized, editingPolicy?.name);
			onOpenChange(false);
		} catch (error) {
			toast.error(getErrorMessage(error));
		} finally {
			setIsSaving(false);
		}
	};

	return (
		<Sheet open={open} onOpenChange={onOpenChange}>
			<SheetContent className="flex w-full flex-col p-0 sm:max-w-2xl" data-testid="circuit-breaker-policy-sheet">
				<SheetHeader headerClassName="mb-0 border-b px-6 py-5" className="flex-col items-start gap-1 sm:flex-row sm:gap-4">
					<SheetTitle className="sm:w-40 sm:shrink-0">
						{editingPolicy ? i18n.t("workspace.circuitBreaker.editPolicy") : i18n.t("workspace.circuitBreaker.createPolicy")}
					</SheetTitle>
					<SheetDescription className="sm:pt-0.5">{i18n.t("workspace.circuitBreaker.sheetDescription")}</SheetDescription>
				</SheetHeader>

				<div className="min-h-0 flex-1 space-y-6 overflow-y-auto px-6 py-5">
					<section className="space-y-4">
						<div className="flex items-center justify-between gap-4">
							<div>
								<h3 className="text-sm font-medium">{i18n.t("workspace.circuitBreaker.identity")}</h3>
								<p className="text-muted-foreground text-xs">{i18n.t("workspace.circuitBreaker.identityDescription")}</p>
							</div>
							<div className="flex items-center gap-2">
								<Label htmlFor="circuit-policy-enabled" className="text-xs">
									{i18n.t("workspace.circuitBreaker.enablePolicy")}
								</Label>
								<Switch
									id="circuit-policy-enabled"
									checked={policy.enabled !== false}
									onCheckedChange={(checked) => updatePolicy("enabled", checked)}
									data-testid="circuit-breaker-policy-enabled-switch"
								/>
							</div>
						</div>
						<div className="space-y-1.5">
							<Label htmlFor="circuit-policy-name">{i18n.t("workspace.circuitBreaker.name")}</Label>
							<Input
								id="circuit-policy-name"
								value={policy.name}
								onChange={(event) => updatePolicy("name", event.target.value)}
								placeholder="azure-ptu-spillover"
								data-testid="circuit-breaker-policy-name-input"
							/>
						</div>
					</section>

					<Separator />

					<section className="space-y-4">
						<div>
							<h3 className="text-sm font-medium">{i18n.t("workspace.circuitBreaker.trafficRoute")}</h3>
							<p className="text-muted-foreground text-xs">{i18n.t("workspace.circuitBreaker.trafficRouteDescription")}</p>
						</div>
						<div className="grid grid-cols-1 items-start gap-3 sm:grid-cols-[1fr_auto_1fr]">
							<div className="space-y-3 rounded-md border p-3">
								<div className="text-muted-foreground text-[11px] font-semibold tracking-wide uppercase">
									{i18n.t("workspace.circuitBreaker.primary")}
								</div>
								<div className="space-y-1.5">
									<Label className="text-xs">{i18n.t("workspace.circuitBreaker.provider")}</Label>
									<ComboboxSelect
										options={providerOptions}
										value={policy.primary_provider || null}
										onValueChange={(value) => {
											updatePolicy("primary_provider", value ?? "");
											updatePolicy("primary_model", "");
											updatePolicy("primary_key_ids", []);
										}}
										placeholder={i18n.t("workspace.circuitBreaker.selectProvider")}
										noPortal
										data-testid="circuit-breaker-primary-provider-select"
									/>
								</div>
								<div className="space-y-1.5">
									<Label className="text-xs">{i18n.t("workspace.circuitBreaker.model")}</Label>
									<ModelMultiselect
										provider={policy.primary_provider || undefined}
										value={policy.primary_model}
										onChange={(value) => updatePolicy("primary_model", value)}
										isSingleSelect
										unfiltered
										clearable
										disabled={!policy.primary_provider}
										placeholder={i18n.t("workspace.circuitBreaker.selectModel")}
										menuPosition="absolute"
										data-testid="circuit-breaker-primary-model-select"
									/>
								</div>
							</div>
							<div className="text-muted-foreground flex h-full items-center justify-center sm:justify-start sm:pt-16">
								<ArrowRight className="size-4 rotate-90 sm:rotate-0" />
							</div>
							<div className="space-y-3 rounded-md border p-3">
								<div className="text-muted-foreground text-[11px] font-semibold tracking-wide uppercase">
									{i18n.t("workspace.circuitBreaker.fallback")}
								</div>
								<div className="space-y-1.5">
									<Label className="text-xs">{i18n.t("workspace.circuitBreaker.provider")}</Label>
									<ComboboxSelect
										options={providerOptions}
										value={policy.fallback_provider || null}
										onValueChange={(value) => {
											updatePolicy("fallback_provider", value ?? "");
											updatePolicy("fallback_model", "");
										}}
										placeholder={i18n.t("workspace.circuitBreaker.selectProvider")}
										noPortal
										data-testid="circuit-breaker-fallback-provider-select"
									/>
								</div>
								<div className="space-y-1.5">
									<Label className="text-xs">{i18n.t("workspace.circuitBreaker.model")}</Label>
									<ModelMultiselect
										provider={policy.fallback_provider || undefined}
										value={policy.fallback_model}
										onChange={(value) => updatePolicy("fallback_model", value)}
										isSingleSelect
										unfiltered
										clearable
										disabled={!policy.fallback_provider}
										placeholder={i18n.t("workspace.circuitBreaker.selectModel")}
										menuPosition="absolute"
										data-testid="circuit-breaker-fallback-model-select"
									/>
								</div>
							</div>
						</div>
						<div className="space-y-1.5">
							<Label>{i18n.t("workspace.circuitBreaker.keySubCircuits")}</Label>
							<ComboboxSelect
								multiple
								options={keyOptions}
								value={policy.primary_key_ids ?? []}
								onValueChange={(value) => updatePolicy("primary_key_ids", value)}
								placeholder={i18n.t("workspace.circuitBreaker.allKeysShared")}
								disabled={!policy.primary_provider}
								compactTrigger
								noPortal
								data-testid="circuit-breaker-key-select"
							/>
							<p className="text-muted-foreground text-xs">{i18n.t("workspace.circuitBreaker.keySubCircuitsDescription")}</p>
						</div>
					</section>

					<Separator />

					<section className="space-y-4">
						<div className="flex items-start justify-between gap-4">
							<div>
								<h3 className="text-sm font-medium">{i18n.t("workspace.circuitBreaker.signals")}</h3>
								<p className="text-muted-foreground text-xs">{i18n.t("workspace.circuitBreaker.signalsDescription")}</p>
							</div>
							<Select
								value={policy.condition.operator ?? "OR"}
								onValueChange={(value) =>
									setPolicy((current) => ({ ...current, condition: { ...current.condition, operator: value as CircuitBreakerOperator } }))
								}
							>
								<SelectTrigger className="w-28" data-testid="circuit-breaker-operator-select">
									<SelectValue />
								</SelectTrigger>
								<SelectContent>
									<SelectItem value="OR">OR · Any</SelectItem>
									<SelectItem value="AND">AND · All</SelectItem>
								</SelectContent>
							</Select>
						</div>
						<div className="space-y-3">
							{policy.condition.signals.map((signal, index) => {
								const mode = getSignalMatchMode(signal);
								return (
									<div key={index} className="rounded-md border p-3" data-testid={`circuit-breaker-signal-${index}`}>
										<div className="mb-3 flex items-center justify-between">
											<span className="text-muted-foreground text-xs font-medium">
												{i18n.t("workspace.circuitBreaker.signalNumber", { index: index + 1 })}
											</span>
											<Button
												type="button"
												variant="ghost"
												size="icon"
												className="size-7"
												disabled={policy.condition.signals.length === 1}
												onClick={() => removeSignal(index)}
												aria-label={i18n.t("workspace.circuitBreaker.removeSignal")}
												data-testid={`circuit-breaker-signal-${index}-remove-btn`}
											>
												<Trash2 className="size-3.5" />
											</Button>
										</div>
										<div className="grid grid-cols-1 gap-3 sm:grid-cols-[1.2fr_.8fr_1fr]">
											<div className="space-y-1.5">
												<Label className="text-xs">{i18n.t("workspace.circuitBreaker.headerName")}</Label>
												<Input
													value={signal.header_name}
													onChange={(event) => updateSignal(index, { ...signal, header_name: event.target.value })}
													placeholder="X-Ms-Is-Spilled-Over"
													data-testid={`circuit-breaker-signal-${index}-header-input`}
												/>
											</div>
											<div className="space-y-1.5">
												<Label className="text-xs">{i18n.t("workspace.circuitBreaker.match")}</Label>
												<Select
													value={mode}
													onValueChange={(value) => updateSignal(index, updateSignalMode(signal, value as CircuitBreakerMatchMode))}
												>
													<SelectTrigger data-testid={`circuit-breaker-signal-${index}-match-select`}>
														<SelectValue />
													</SelectTrigger>
													<SelectContent>
														<SelectItem value="exists">{i18n.t("workspace.circuitBreaker.exists")}</SelectItem>
														<SelectItem value="equals">{i18n.t("workspace.circuitBreaker.equals")}</SelectItem>
														<SelectItem value="contains">{i18n.t("workspace.circuitBreaker.contains")}</SelectItem>
													</SelectContent>
												</Select>
											</div>
											<div className="space-y-1.5">
												<Label className="text-xs">{i18n.t("workspace.circuitBreaker.value")}</Label>
												<Input
													disabled={mode === "exists"}
													value={signal.header_value ?? signal.header_contains ?? ""}
													onChange={(event) =>
														updateSignal(
															index,
															mode === "equals"
																? { ...signal, header_value: event.target.value }
																: { ...signal, header_contains: event.target.value },
														)
													}
													placeholder={mode === "exists" ? i18n.t("workspace.circuitBreaker.anyValue") : "true"}
													data-testid={`circuit-breaker-signal-${index}-value-input`}
												/>
											</div>
										</div>
									</div>
								);
							})}
						</div>
						<Button type="button" variant="outline" size="sm" onClick={addSignal} data-testid="add-circuit-breaker-signal-btn">
							<Plus className="size-4" />
							{i18n.t("workspace.circuitBreaker.addSignal")}
						</Button>
					</section>

					<Separator />

					<section className="space-y-4">
						<div>
							<h3 className="text-sm font-medium">{i18n.t("workspace.circuitBreaker.recovery")}</h3>
							<p className="text-muted-foreground text-xs">{i18n.t("workspace.circuitBreaker.recoveryDescription")}</p>
						</div>
						<div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
							<div className="space-y-1.5">
								<Label htmlFor="circuit-default-cooldown">{i18n.t("workspace.circuitBreaker.defaultCooldown")}</Label>
								<Input
									id="circuit-default-cooldown"
									value={policy.default_cooldown ?? "30s"}
									onChange={(event) => updatePolicy("default_cooldown", event.target.value)}
									placeholder="30s"
									data-testid="circuit-breaker-cooldown-input"
								/>
								<p className="text-muted-foreground text-xs">30s, 5m, 1h</p>
							</div>
							<div className="space-y-1.5">
								<Label htmlFor="circuit-cooldown-header">{i18n.t("workspace.circuitBreaker.cooldownHeader")}</Label>
								<Input
									id="circuit-cooldown-header"
									value={policy.cooldown_header ?? ""}
									onChange={(event) => updatePolicy("cooldown_header", event.target.value)}
									placeholder="retry-after-ms"
									data-testid="circuit-breaker-cooldown-header-input"
								/>
								<p className="text-muted-foreground text-xs">{i18n.t("workspace.circuitBreaker.cooldownHeaderHint")}</p>
							</div>
						</div>
					</section>

					{validationError && (
						<div
							className="border-destructive/30 bg-destructive/5 text-destructive flex items-start gap-2 rounded-md border px-3 py-2 text-sm"
							role="alert"
						>
							<AlertCircle className="mt-0.5 size-4 shrink-0" />
							{validationError}
						</div>
					)}
				</div>

				<div className="flex items-center justify-end gap-2 border-t px-6 py-4">
					<Button variant="outline" onClick={() => onOpenChange(false)} disabled={isSaving} data-testid="cancel-circuit-breaker-policy-btn">
						{i18n.t("common.cancel")}
					</Button>
					<Button onClick={handleSave} disabled={isSaving} data-testid="save-circuit-breaker-policy-btn">
						{isSaving ? i18n.t("common.saving") : i18n.t("common.save")}
					</Button>
				</div>
			</SheetContent>
		</Sheet>
	);
}