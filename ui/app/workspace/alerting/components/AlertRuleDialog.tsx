import { Checkbox } from "@/components/ui/checkbox";
import { Button } from "@/components/ui/button";
import { CELRuleBuilder, type CELFieldDefinition, type CELOperatorDefinition } from "@/components/ui/custom/celBuilder";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { CustomerSelector } from "@/components/entitySelectors/customerSelector";
import { TeamSelector } from "@/components/entitySelectors/teamSelector";
import { VirtualKeySelector } from "@/components/entitySelectors/virtualKeySelector";
import i18n from "@/lib/i18n";
import { getErrorMessage, useCreateAlertRuleMutation, useGetModelsQuery, useUpdateAlertRuleMutation } from "@/lib/store";
import type { AlertChannel, AlertRule, AlertScopeType } from "@/lib/types/alerting";
import { BellRing, Braces, ChartNoAxesCombined, CircleGauge, Target } from "lucide-react";
import { FormEvent, ReactNode, useEffect, useMemo, useState } from "react";
import type { RuleGroupType, RuleType } from "react-querybuilder";
import { toast } from "sonner";
import { durationFromSeconds, durationToSeconds, ruleRequest, type DurationUnit, type RuleFormValue } from "./alertingModel";
import { alertMetricsForScope } from "./alertingRuleFields";
import { alertingCopy } from "./copy";

const copy = alertingCopy();
const tr = (key: string) => i18n.t(`workspace.alerting.${key}`);

const alertOperators: CELOperatorDefinition[] = [
	{ name: ">", label: tr("operatorGreaterThan"), celSyntax: ">" },
	{ name: ">=", label: tr("operatorGreaterThanOrEqual"), celSyntax: ">=" },
	{ name: "<", label: tr("operatorLessThan"), celSyntax: "<" },
	{ name: "<=", label: tr("operatorLessThanOrEqual"), celSyntax: "<=" },
	{ name: "==", label: tr("operatorEquals"), celSyntax: "==" },
	{ name: "!=", label: tr("operatorNotEquals"), celSyntax: "!=" },
];

const governanceQuery: RuleGroupType = {
	combinator: "and",
	rules: [{ field: "budget_usage_percent", operator: ">=", value: 80 }],
};
const providerQuery: RuleGroupType = {
	combinator: "and",
	rules: [{ field: "provider_error_rate", operator: ">=", value: 10 }],
};

const emptyRule: RuleFormValue = {
	name: "",
	description: "",
	enabled: true,
	scope_type: "virtual_key",
	scope_id: "",
	target_type: undefined,
	target_id: "",
	cel_expression: "budget_usage_percent >= 80",
	channel_ids: [],
	cooldown_seconds: 60,
	window_seconds: 300,
	min_requests: 100,
	notify_once_per_reset_cycle: false,
	query: governanceQuery as unknown as Record<string, unknown>,
};

function alertQueryToCEL(group: RuleGroupType | undefined): string {
	if (!group?.rules?.length) return "";
	const expressions = group.rules
		.map((entry) => {
			if ("rules" in entry) {
				const nested = alertQueryToCEL(entry as RuleGroupType);
				return nested ? `(${nested})` : "";
			}
			const rule = entry as RuleType;
			if (!rule.field || !rule.operator || rule.value === "") return "";
			return `${rule.field} ${rule.operator} ${Number(rule.value) || 0}`;
		})
		.filter(Boolean);
	return expressions.join(String(group.combinator).toLowerCase() === "or" ? " || " : " && ");
}

function Section({ icon, title, description, children }: { icon: ReactNode; title: string; description: string; children: ReactNode }) {
	return (
		<section className="grid gap-5 border-b px-6 py-5 last:border-b-0 lg:grid-cols-[13rem_minmax(0,1fr)] lg:gap-8">
			<div>
				<div className="flex items-center gap-2 text-sm font-semibold">
					<span className="text-muted-foreground">{icon}</span>
					{title}
				</div>
				<p className="text-muted-foreground mt-1.5 text-xs leading-relaxed">{description}</p>
			</div>
			<div className="min-w-0">{children}</div>
		</section>
	);
}

function Field({ label, description, children }: { label: string; description?: string; children: ReactNode }) {
	return (
		<div className="space-y-1.5">
			<Label>{label}</Label>
			{children}
			{description ? <p className="text-muted-foreground text-xs leading-relaxed">{description}</p> : null}
		</div>
	);
}

function DurationField({
	label,
	description,
	value,
	unit,
	allowZero,
	testId,
	onChange,
}: {
	label: string;
	description: string;
	value: string;
	unit: DurationUnit;
	allowZero?: boolean;
	testId: string;
	onChange: (value: string, unit: DurationUnit) => void;
}) {
	return (
		<Field label={label} description={description}>
			<div className="flex min-w-0">
				<Input
					className="rounded-r-none"
					data-testid={`${testId}-value`}
					type="number"
					min={allowZero ? 0 : 1}
					value={value}
					onChange={(event) => onChange(event.target.value, unit)}
				/>
				<Select value={unit} onValueChange={(nextUnit) => onChange(value, nextUnit as DurationUnit)}>
					<SelectTrigger className="w-28 shrink-0 rounded-l-none border-l-0" data-testid={`${testId}-unit`}>
						<SelectValue />
					</SelectTrigger>
					<SelectContent>
						{(["minutes", "hours", "days"] as DurationUnit[]).map((item) => (
							<SelectItem key={item} value={item}>
								{copy.units[item]}
							</SelectItem>
						))}
					</SelectContent>
				</Select>
			</div>
		</Field>
	);
}

export function AlertRuleDialog({
	open,
	onOpenChange,
	rule,
	channels,
	providers,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	rule: AlertRule | null;
	channels: AlertChannel[];
	providers: string[];
}) {
	const [form, setForm] = useState<RuleFormValue>(emptyRule);
	const [windowUnit, setWindowUnit] = useState<DurationUnit>("minutes");
	const [windowValue, setWindowValue] = useState("5");
	const [cooldownUnit, setCooldownUnit] = useState<DurationUnit>("minutes");
	const [cooldownValue, setCooldownValue] = useState("1");
	const [create, creating] = useCreateAlertRuleMutation();
	const [update, updating] = useUpdateAlertRuleMutation();
	const { data: providerModels } = useGetModelsQuery(
		{ provider: form.scope_id, unfiltered: true, limit: 1000 },
		{ skip: form.scope_type !== "provider" || !form.scope_id },
	);

	const query = (form.query as unknown as RuleGroupType | undefined) ?? { combinator: "and", rules: [] };
	const builderFields = useMemo<CELFieldDefinition[]>(
		() =>
			alertMetricsForScope(form.scope_type).map(([name, labelKey]) => ({
				name,
				label: tr(labelKey),
				inputType: "number",
				operators: alertOperators.map((operator) => operator.name),
				defaultOperator: ">=",
				defaultValue: 0,
			})),
		[form.scope_type],
	);

	useEffect(() => {
		if (!open) return;
		if (!rule) {
			setForm(emptyRule);
			setWindowValue("5");
			setWindowUnit("minutes");
			setCooldownValue("1");
			setCooldownUnit("minutes");
			return;
		}
		const windowDuration = durationFromSeconds(rule.window_seconds);
		const cooldownDuration = durationFromSeconds(rule.cooldown_milliseconds / 1000, true);
		setWindowValue(windowDuration.value);
		setWindowUnit(windowDuration.unit);
		setCooldownValue(cooldownDuration.value);
		setCooldownUnit(cooldownDuration.unit);
		setForm({
			name: rule.name,
			description: rule.description ?? "",
			enabled: rule.enabled,
			scope_type: rule.scope_type,
			scope_id: rule.scope_id,
			target_type: rule.target_type,
			target_id: rule.target_id,
			cel_expression: rule.cel_expression,
			channel_ids: rule.channel_ids,
			cooldown_seconds: rule.cooldown_milliseconds / 1000,
			window_seconds: rule.window_seconds,
			min_requests: rule.min_requests,
			notify_once_per_reset_cycle: rule.notify_once_per_reset_cycle,
			query: rule.query,
		});
	}, [open, rule]);

	const setRuleKind = (providerRule: boolean) => {
		const nextQuery = providerRule ? providerQuery : governanceQuery;
		setForm((current) => {
			if ((current.scope_type === "provider") === providerRule) return current;
			return {
				...current,
				scope_type: providerRule ? "provider" : "virtual_key",
				scope_id: "",
				target_id: "",
				target_type: undefined,
				cel_expression: providerRule ? "provider_error_rate >= 10" : "budget_usage_percent >= 80",
				query: nextQuery as unknown as Record<string, unknown>,
				notify_once_per_reset_cycle: false,
			};
		});
	};

	const submit = async (event: FormEvent) => {
		event.preventDefault();
		try {
			const request = ruleRequest({
				...form,
				window_seconds: durationToSeconds(windowValue, windowUnit),
				cooldown_seconds: durationToSeconds(cooldownValue, cooldownUnit, true),
				notify_once_per_reset_cycle: form.scope_type === "provider" ? false : form.notify_once_per_reset_cycle,
			});
			if (rule) await update({ id: rule.id, data: request }).unwrap();
			else await create(request).unwrap();
			toast.success(copy.ruleSaved);
			onOpenChange(false);
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	};

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className="h-[calc(100dvh-2rem)] max-h-[calc(100dvh-2rem)] grid-rows-[minmax(0,1fr)] gap-0 overflow-hidden p-0 sm:max-w-5xl">
				<form onSubmit={submit} className="flex h-full min-h-0 flex-col overflow-hidden">
					<DialogHeader className="shrink-0 border-b px-6 py-5 pr-14">
						<DialogTitle>{rule ? tr("editRule") : tr("createRule")}</DialogTitle>
						<DialogDescription className="max-w-2xl">{copy.rulesDescription}</DialogDescription>
					</DialogHeader>

					<div className="min-h-0 flex-1 overflow-y-auto overscroll-contain">
						<Section icon={<Target className="size-4" />} title={tr("ruleKindTitle")} description={tr("ruleKindDescription")}>
							<div className="grid gap-2 sm:grid-cols-2" data-testid="alert-rule-kind-selector">
								<Button
									type="button"
									variant="outline"
									data-testid="alert-rule-kind-governance"
									aria-pressed={form.scope_type !== "provider"}
									onClick={() => setRuleKind(false)}
									className="aria-pressed:border-primary/50 aria-pressed:bg-primary/5 hover:bg-muted/40 h-auto justify-start p-3 text-left whitespace-normal"
								>
									<span className="min-w-0">
										<span className="block text-sm font-semibold">{tr("governanceRuleType")}</span>
										<span className="text-muted-foreground mt-1 block text-xs leading-relaxed">{tr("governanceRuleTypeDescription")}</span>
									</span>
								</Button>
								<Button
									type="button"
									variant="outline"
									data-testid="alert-rule-kind-provider-failures"
									aria-pressed={form.scope_type === "provider"}
									onClick={() => setRuleKind(true)}
									className="aria-pressed:border-primary/50 aria-pressed:bg-primary/5 hover:bg-muted/40 h-auto justify-start p-3 text-left whitespace-normal"
								>
									<span className="min-w-0">
										<span className="block text-sm font-semibold">{tr("providerFailureRuleType")}</span>
										<span className="text-muted-foreground mt-1 block text-xs leading-relaxed">
											{tr("providerFailureRuleTypeDescription")}
										</span>
									</span>
								</Button>
							</div>
						</Section>

						<Section icon={<CircleGauge className="size-4" />} title={tr("ruleIdentityTitle")} description={tr("ruleIdentityDescription")}>
							<div className="grid gap-4 md:grid-cols-2">
								<Field label={copy.name}>
									<Input
										data-testid="alert-rule-name"
										required
										value={form.name}
										onChange={(event) => setForm({ ...form, name: event.target.value })}
									/>
								</Field>
								<Field label={copy.description}>
									<Input
										data-testid="alert-rule-description"
										value={form.description}
										onChange={(event) => setForm({ ...form, description: event.target.value })}
									/>
								</Field>
								<Field label={tr("scopeType")}>
									<Select
										value={form.scope_type}
										onValueChange={(value) => {
											const providerRule = value === "provider";
											const changesMetricFamily = (form.scope_type === "provider") !== providerRule;
											const nextQuery = providerRule ? providerQuery : governanceQuery;
											setForm({
												...form,
												scope_type: value as AlertScopeType,
												scope_id: "",
												target_id: "",
												target_type: undefined,
												cel_expression: changesMetricFamily
													? providerRule
														? "provider_error_rate >= 10"
														: "budget_usage_percent >= 80"
													: form.cel_expression,
												query: changesMetricFamily ? (nextQuery as unknown as Record<string, unknown>) : form.query,
											});
										}}
									>
										<SelectTrigger className="w-full" data-testid="alert-rule-scope">
											<SelectValue />
										</SelectTrigger>
										<SelectContent>
											{(["virtual_key", "team", "customer", "provider"] as AlertScopeType[]).map((scope) => (
												<SelectItem key={scope} value={scope}>
													{copy.scopeLabels[scope]}
												</SelectItem>
											))}
										</SelectContent>
									</Select>
								</Field>
								<Field label={form.scope_type === "provider" ? copy.provider : copy.scopeId}>
									<div>
										{form.scope_type === "provider" ? (
											<Select value={form.scope_id} onValueChange={(scope_id) => setForm({ ...form, scope_id, target_id: "" })}>
												<SelectTrigger className="w-full" data-testid="alert-rule-scope-id">
													<SelectValue placeholder={tr("selectProvider")} />
												</SelectTrigger>
												<SelectContent>
													{providers.map((provider) => (
														<SelectItem key={provider} value={provider}>
															{provider}
														</SelectItem>
													))}
												</SelectContent>
											</Select>
										) : form.scope_type === "virtual_key" ? (
											<VirtualKeySelector
												data-testid="alert-rule-scope-id"
												value={form.scope_id}
												onChange={(scope_id) => setForm({ ...form, scope_id })}
											/>
										) : form.scope_type === "team" ? (
											<TeamSelector
												data-testid="alert-rule-scope-id"
												value={form.scope_id}
												onChange={(scope_id) => setForm({ ...form, scope_id })}
											/>
										) : (
											<CustomerSelector
												data-testid="alert-rule-scope-id"
												value={form.scope_id}
												onChange={(scope_id) => setForm({ ...form, scope_id })}
											/>
										)}
									</div>
								</Field>
								<Field label={form.scope_type === "provider" ? copy.modelTarget : copy.budgetTarget}>
									{form.scope_type === "provider" ? (
										<Select
											value={form.target_id || "__all__"}
											onValueChange={(value) => setForm({ ...form, target_id: value === "__all__" ? "" : value })}
										>
											<SelectTrigger className="w-full" data-testid="alert-rule-target-id">
												<SelectValue placeholder={tr("allModels")} />
											</SelectTrigger>
											<SelectContent>
												<SelectItem value="__all__">{tr("allModels")}</SelectItem>
												{(providerModels?.models ?? []).map((model) => (
													<SelectItem key={model.name} value={model.name}>
														{model.name}
													</SelectItem>
												))}
											</SelectContent>
										</Select>
									) : (
										<Input
											data-testid="alert-rule-target-id"
											placeholder={tr("anyBudget")}
											value={form.target_id ?? ""}
											onChange={(event) => setForm({ ...form, target_id: event.target.value })}
										/>
									)}
								</Field>
								<label className="bg-muted/25 flex min-h-16 items-center justify-between gap-4 rounded-md border px-4 py-3">
									<span>
										<span className="block text-sm font-medium">{copy.enabled}</span>
										<span className="text-muted-foreground block text-xs">{tr("evaluateEachSweep")}</span>
									</span>
									<Switch
										data-testid="alert-rule-enabled"
										checked={form.enabled}
										onCheckedChange={(enabled) => setForm({ ...form, enabled })}
									/>
								</label>
							</div>
						</Section>

						<Section
							icon={<ChartNoAxesCombined className="size-4" />}
							title={tr("statisticsAndNoise")}
							description={tr("statisticsAndNoiseDescription")}
						>
							{form.scope_type === "provider" ? (
								<div className="grid gap-4 md:grid-cols-3">
									<DurationField
										label={tr("rollingWindow")}
										description={tr("rollingWindowDescription")}
										value={windowValue}
										unit={windowUnit}
										testId="alert-rule-window"
										onChange={(value, unit) => {
											setWindowValue(value);
											setWindowUnit(unit);
										}}
									/>
									<Field label={tr("minimumSampleSize")} description={tr("minimumSampleDescription")}>
										<Input
											data-testid="alert-rule-min-requests"
											type="number"
											min={1}
											value={form.min_requests}
											onChange={(event) => setForm({ ...form, min_requests: Number(event.target.value) })}
										/>
									</Field>
									<DurationField
										label={tr("notificationCooldown")}
										description={tr("notificationCooldownDescription")}
										value={cooldownValue}
										unit={cooldownUnit}
										allowZero
										testId="alert-rule-cooldown"
										onChange={(value, unit) => {
											setCooldownValue(value);
											setCooldownUnit(unit);
										}}
									/>
								</div>
							) : (
								<div className="grid gap-4 md:grid-cols-2">
									<DurationField
										label={copy.cooldown}
										description={tr("ruleCooldownDescription")}
										value={cooldownValue}
										unit={cooldownUnit}
										allowZero
										testId="alert-rule-cooldown"
										onChange={(value, unit) => {
											setCooldownValue(value);
											setCooldownUnit(unit);
										}}
									/>
									<label className="bg-muted/25 flex items-center justify-between gap-4 rounded-md border px-4 py-3">
										<span>
											<span className="block text-sm font-medium">{copy.notifyOnce}</span>
											<span className="text-muted-foreground block text-xs">{tr("notifyOnceDescription")}</span>
										</span>
										<Switch
											data-testid="alert-rule-notify-once"
											checked={form.notify_once_per_reset_cycle}
											onCheckedChange={(notify_once_per_reset_cycle) => setForm({ ...form, notify_once_per_reset_cycle })}
										/>
									</label>
								</div>
							)}
						</Section>

						<Section icon={<Braces className="size-4" />} title={tr("condition")} description={tr("conditionBuilderDescription")}>
							<div className="min-w-0" data-testid="alert-rule-condition-builder">
								<CELRuleBuilder
									key={`${rule?.id ?? "new"}-${form.scope_type}`}
									fields={builderFields}
									operators={alertOperators}
									convertToCEL={alertQueryToCEL}
									initialQuery={query}
									initialCel={form.cel_expression}
									celTextareaTestId="alert-rule-cel"
									initialMode={rule && !rule.query ? "cel" : "builder"}
									allowCelMode
									onChange={(cel_expression, nextQuery) =>
										setForm((current) => ({ ...current, cel_expression, query: nextQuery as unknown as Record<string, unknown> }))
									}
								/>
							</div>
						</Section>

						<Section icon={<BellRing className="size-4" />} title={copy.channels} description={tr("channelsDescription")}>
							<div className="grid gap-2 sm:grid-cols-2">
								{channels.map((channel) => (
									<label
										key={channel.id}
										className="hover:bg-muted/40 has-data-[state=checked]:border-primary/40 has-data-[state=checked]:bg-primary/5 flex cursor-pointer items-center gap-3 rounded-md border px-3 py-2.5 transition-colors duration-150"
									>
										<Checkbox
											data-testid={`alert-rule-channel-${channel.id}`}
											checked={form.channel_ids.includes(channel.id)}
											onCheckedChange={(checked) =>
												setForm({
													...form,
													channel_ids: checked ? [...form.channel_ids, channel.id] : form.channel_ids.filter((id) => id !== channel.id),
												})
											}
										/>
										<span className="min-w-0">
											<span className="block truncate text-sm font-medium">{channel.name}</span>
											<span className="text-muted-foreground block text-xs">{channel.type}</span>
										</span>
									</label>
								))}
								{channels.length === 0 ? (
									<p className="text-muted-foreground col-span-full text-sm">{tr("noChannelsDescription")}</p>
								) : null}
							</div>
						</Section>
					</div>

					<DialogFooter className="bg-background shrink-0 border-t px-6 py-4">
						<Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
							{copy.cancel}
						</Button>
						<Button
							type="submit"
							data-testid="alert-rule-save"
							disabled={creating.isLoading || updating.isLoading || form.channel_ids.length === 0}
						>
							{rule ? tr("saveChanges") : tr("createRule")}
						</Button>
					</DialogFooter>
				</form>
			</DialogContent>
		</Dialog>
	);
}