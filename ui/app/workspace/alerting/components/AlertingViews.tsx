import i18n from "@/lib/i18n";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import {
	getErrorMessage,
	useCreateAlertChannelMutation,
	useCreateAlertRuleMutation,
	useDeleteAlertChannelMutation,
	useDeleteAlertRuleMutation,
	useEvaluateAlertRuleMutation,
	useEvaluateAlertsMutation,
	useGetAlertChannelsQuery,
	useGetAlertHistoryQuery,
	useGetAlertRulesQuery,
	useGetProvidersQuery,
	useTestAlertChannelMutation,
	useUpdateAlertChannelMutation,
	useUpdateAlertRuleMutation,
} from "@/lib/store";
import type { AlertChannel, AlertChannelFormType, AlertHistoryRecord, AlertRule, AlertScopeType, AlertStatus } from "@/lib/types/alerting";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { BellRing, ChevronLeft, ChevronRight, Pencil, Plus, RefreshCw, Send, SlidersHorizontal, Trash2 } from "lucide-react";
import { FormEvent, ReactNode, useEffect, useState } from "react";
import { toast } from "sonner";
import {
	ChannelFormValue,
	channelRequest,
	durationFromSeconds,
	durationToSeconds,
	RuleFormValue,
	ruleRequest,
	safeHistoryDetail,
	type DurationUnit,
} from "./alertingModel";
import { alertingCopy } from "./copy";

const copy = alertingCopy();
function Header({ title, description, action }: { title: string; description: string; action?: ReactNode }) {
	return (
		<div className="mb-5 flex items-center justify-between">
			<div>
				<h1 className="text-lg font-semibold">
					{title} <Badge variant="outline">{copy.beta}</Badge>
				</h1>
				<p className="text-muted-foreground text-sm">{description}</p>
			</div>
			{action}
		</div>
	);
}
function Empty() {
	return (
		<div className="text-muted-foreground flex min-h-72 items-center justify-center rounded-sm border border-dashed">{copy.noItems}</div>
	);
}

const emptyChannel: ChannelFormValue = { name: "", description: "", type: "slack", destination: "", headers: "{}", enabled: true };
function ChannelDialog({
	open,
	onOpenChange,
	channel,
}: {
	open: boolean;
	onOpenChange: (v: boolean) => void;
	channel: AlertChannel | null;
}) {
	const [form, setForm] = useState(emptyChannel);
	const [create, creating] = useCreateAlertChannelMutation();
	const [update, updating] = useUpdateAlertChannelMutation();
	useEffect(() => {
		if (open)
			setForm(
				channel
					? {
							name: channel.name,
							description: channel.description ?? "",
							type: channel.type,
							destination: "",
							headers: "{}",
							enabled: channel.enabled,
						}
					: emptyChannel,
			);
	}, [open, channel]);
	const submit = async (event: FormEvent) => {
		event.preventDefault();
		try {
			const request = channelRequest(form, Boolean(channel));
			if (channel) await update({ id: channel.id, data: request }).unwrap();
			else await create(request).unwrap();
			toast.success(copy.channelSaved);
			onOpenChange(false);
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	};
	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className="sm:max-w-xl">
				<form onSubmit={submit} className="space-y-4">
					<DialogHeader>
						<DialogTitle>
							{channel ? copy.edit : copy.add} {copy.channels}
						</DialogTitle>
						<DialogDescription>{copy.channelsDescription}</DialogDescription>
					</DialogHeader>
					<div className="grid gap-2">
						<Label>{copy.name}</Label>
						<Input
							data-testid="alert-channel-name"
							value={form.name}
							onChange={(e) => setForm({ ...form, name: e.target.value })}
							required
						/>
					</div>
					<div className="grid gap-2">
						<Label>{copy.type}</Label>
						<Select value={form.type} onValueChange={(type) => setForm({ ...form, type: type as AlertChannelFormType })}>
							<SelectTrigger data-testid="alert-channel-type">
								<SelectValue />
							</SelectTrigger>
							<SelectContent>
								{[
									["slack", "Slack"],
									["microsoft_teams", "Microsoft Teams"],
									["wecom", "WeCom"],
									["pagerduty", "PagerDuty"],
									["webhook", copy.genericWebhook],
								].map(([v, l]) => (
									<SelectItem key={v} value={v}>
										{l}
									</SelectItem>
								))}
							</SelectContent>
						</Select>
					</div>
					<div className="grid gap-2">
						<Label>{form.type === "pagerduty" ? copy.routingKey : copy.webhookUrl}</Label>
						<Input
							data-testid="alert-channel-destination"
							type="password"
							value={form.destination}
							onChange={(e) => setForm({ ...form, destination: e.target.value })}
							placeholder={channel ? copy.keepSecret : "https://…"}
						/>
						<p className="text-muted-foreground text-xs">{copy.privateWarning}</p>
					</div>
					{form.type === "webhook" ? (
						<div className="grid gap-2">
							<Label>{copy.headersJson}</Label>
							<Textarea
								data-testid="alert-channel-headers"
								value={form.headers}
								onChange={(e) => setForm({ ...form, headers: e.target.value })}
							/>
						</div>
					) : null}
					<div className="grid gap-2">
						<Label>{copy.description}</Label>
						<Input value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
					</div>
					<div className="flex items-center justify-between">
						<Label>{copy.enabled}</Label>
						<Switch checked={form.enabled} onCheckedChange={(enabled) => setForm({ ...form, enabled })} />
					</div>
					<DialogFooter>
						<Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
							{copy.cancel}
						</Button>
						<Button type="submit" disabled={creating.isLoading || updating.isLoading} data-testid="alert-channel-save">
							{copy.save}
						</Button>
					</DialogFooter>
				</form>
			</DialogContent>
		</Dialog>
	);
}

export function AlertChannelsView() {
	const canEdit = useRbac(RbacResource.AlertChannels, RbacOperation.Update);
	const { data, isLoading } = useGetAlertChannelsQuery();
	const [dialog, setDialog] = useState(false);
	const [editing, setEditing] = useState<AlertChannel | null>(null);
	const [remove] = useDeleteAlertChannelMutation();
	const [test] = useTestAlertChannelMutation();
	return (
		<div>
			<Header
				title={copy.channels}
				description={copy.channelsDescription}
				action={
					canEdit ? (
						<Button
							data-testid="alert-channels-add"
							onClick={() => {
								setEditing(null);
								setDialog(true);
							}}
						>
							<Plus className="size-4" /> {copy.add}
						</Button>
					) : undefined
				}
			/>
			{isLoading ? (
				<RefreshCw className="animate-spin" />
			) : !data?.channels.length ? (
				<Empty />
			) : (
				<Table>
					<TableHeader>
						<TableRow>
							<TableHead>{copy.name}</TableHead>
							<TableHead>{copy.type}</TableHead>
							<TableHead>{copy.status}</TableHead>
							<TableHead />
						</TableRow>
					</TableHeader>
					<TableBody>
						{data.channels.map((channel) => (
							<TableRow key={channel.id} data-testid="alert-channel-row">
								<TableCell>{channel.name}</TableCell>
								<TableCell>{channel.type}</TableCell>
								<TableCell>
									<Badge variant={channel.enabled ? "default" : "secondary"}>{channel.enabled ? copy.enabled : copy.disabled}</Badge>
								</TableCell>
								<TableCell className="text-right">
									{canEdit && (
										<>
											<Button
												variant="ghost"
												size="icon"
												data-testid="alert-channel-test"
												onClick={async () => {
													try {
														await test(channel.id).unwrap();
														toast.success(copy.testSent);
													} catch (e) {
														toast.error(getErrorMessage(e));
													}
												}}
											>
												<Send className="size-4" />
											</Button>
											<Button
												variant="ghost"
												size="icon"
												onClick={() => {
													setEditing(channel);
													setDialog(true);
												}}
											>
												<Pencil className="size-4" />
											</Button>
											<Button variant="ghost" size="icon" onClick={() => remove(channel.id)}>
												<Trash2 className="size-4" />
											</Button>
										</>
									)}
								</TableCell>
							</TableRow>
						))}
					</TableBody>
				</Table>
			)}
			<ChannelDialog open={dialog} onOpenChange={setDialog} channel={editing} />
		</div>
	);
}

const emptyRule: RuleFormValue = {
	name: "",
	description: "",
	enabled: true,
	scope_type: "provider",
	scope_id: "",
	target_type: undefined,
	target_id: "",
	cel_expression: "provider_error_rate > 0.1",
	channel_ids: [],
	cooldown_seconds: 300,
	window_seconds: 300,
	min_requests: 10,
	notify_once_per_reset_cycle: false,
};
function RuleDialog({
	open,
	onOpenChange,
	rule,
	channels,
	providers,
}: {
	open: boolean;
	onOpenChange: (v: boolean) => void;
	rule: AlertRule | null;
	channels: AlertChannel[];
	providers: string[];
}) {
	const [form, setForm] = useState<RuleFormValue>(emptyRule);
	const [windowUnit, setWindowUnit] = useState<DurationUnit>("minutes");
	const [windowValue, setWindowValue] = useState("5");
	const [cooldownUnit, setCooldownUnit] = useState<DurationUnit>("minutes");
	const [cooldownValue, setCooldownValue] = useState("5");
	const [create] = useCreateAlertRuleMutation();
	const [update] = useUpdateAlertRuleMutation();
	useEffect(() => {
		if (!open) return;
		if (!rule) {
			setForm(emptyRule);
			setWindowValue("5");
			setWindowUnit("minutes");
			setCooldownValue("5");
			setCooldownUnit("minutes");
			return;
		}
		const w = durationFromSeconds(rule.window_seconds);
		const c = durationFromSeconds(rule.cooldown_milliseconds / 1000, true);
		setWindowValue(w.value);
		setWindowUnit(w.unit);
		setCooldownValue(c.value);
		setCooldownUnit(c.unit);
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
	const submit = async (event: FormEvent) => {
		event.preventDefault();
		try {
			const request = ruleRequest({
				...form,
				window_seconds: durationToSeconds(windowValue, windowUnit),
				cooldown_seconds: durationToSeconds(cooldownValue, cooldownUnit, true),
			});
			if (rule) await update({ id: rule.id, data: request }).unwrap();
			else await create(request).unwrap();
			toast.success(copy.ruleSaved);
			onOpenChange(false);
		} catch (e) {
			toast.error(getErrorMessage(e));
		}
	};
	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-3xl">
				<form onSubmit={submit} className="space-y-4">
					<DialogHeader>
						<DialogTitle>
							{rule ? copy.edit : copy.add} {copy.rules}
						</DialogTitle>
						<DialogDescription>{copy.rulesDescription}</DialogDescription>
					</DialogHeader>
					<div className="bg-muted/30 rounded-sm border p-3 text-sm">
						<p className="font-medium">{copy.scope}</p>
						<p className="text-muted-foreground mt-1 text-xs">{copy.rulesDescription}</p>
					</div>
					<div className="grid gap-4 md:grid-cols-2">
						<div className="text-muted-foreground col-span-full text-xs font-semibold tracking-wide uppercase">
							{i18n.t("workspace.alerting.copy.AlertingViews_identity_and_target")}
						</div>
						<div>
							<Label>{copy.name}</Label>
							<Input data-testid="alert-rule-name" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
						</div>
						<div>
							<Label>{copy.scope}</Label>
							<Select
								value={form.scope_type}
								onValueChange={(v) => setForm({ ...form, scope_type: v as AlertScopeType, target_type: undefined, target_id: "" })}
							>
								<SelectTrigger data-testid="alert-rule-scope">
									<SelectValue />
								</SelectTrigger>
								<SelectContent>
									{["provider", "virtual_key", "team", "customer"].map((v) => (
										<SelectItem key={v} value={v}>
											{copy.scopeLabels[v as AlertScopeType]}
										</SelectItem>
									))}
								</SelectContent>
							</Select>
						</div>
						<div>
							<Label>{form.scope_type === "provider" ? copy.provider : copy.scopeId}</Label>
							{form.scope_type === "provider" ? (
								<Select value={form.scope_id} onValueChange={(scope_id) => setForm({ ...form, scope_id })}>
									<SelectTrigger data-testid="alert-rule-scope-id">
										<SelectValue placeholder={copy.provider} />
									</SelectTrigger>
									<SelectContent>
										{providers.map((provider) => (
											<SelectItem key={provider} value={provider}>
												{provider}
											</SelectItem>
										))}
									</SelectContent>
								</Select>
							) : (
								<Input
									data-testid="alert-rule-scope-id"
									value={form.scope_id}
									onChange={(e) => setForm({ ...form, scope_id: e.target.value })}
								/>
							)}
						</div>
						<div>
							<Label>{form.scope_type === "provider" ? copy.modelTarget : copy.budgetTarget}</Label>
							<Input
								data-testid="alert-rule-target-id"
								value={form.target_id ?? ""}
								onChange={(e) => setForm({ ...form, target_id: e.target.value })}
							/>
						</div>
						<div className="col-span-full flex items-center gap-2 border-t pt-4 text-sm font-medium">
							<SlidersHorizontal className="size-4" /> {i18n.t("workspace.alerting.copy.AlertingViews_reliability_controls")}
						</div>
						<div>
							<Label>{copy.window}</Label>
							<div className="flex">
								<Input
									type="number"
									min={1}
									value={windowValue}
									onChange={(e) => setWindowValue(e.target.value)}
									data-testid="alert-rule-window-value"
								/>
								<Select value={windowUnit} onValueChange={(v) => setWindowUnit(v as DurationUnit)}>
									<SelectTrigger className="w-28">
										<SelectValue />
									</SelectTrigger>
									<SelectContent>
										{["minutes", "hours", "days"].map((v) => (
											<SelectItem key={v} value={v}>
												{copy.units[v as DurationUnit]}
											</SelectItem>
										))}
									</SelectContent>
								</Select>
							</div>
						</div>
						<div>
							<Label>{copy.minimumRequests}</Label>
							<Input
								data-testid="alert-rule-min-requests"
								type="number"
								min={1}
								value={form.min_requests}
								onChange={(e) => setForm({ ...form, min_requests: Number(e.target.value) })}
							/>
						</div>
						<div>
							<Label>{copy.cooldown}</Label>
							<div className="flex">
								<Input
									type="number"
									min={0}
									value={cooldownValue}
									onChange={(e) => setCooldownValue(e.target.value)}
									data-testid="alert-rule-cooldown-value"
								/>
								<Select value={cooldownUnit} onValueChange={(v) => setCooldownUnit(v as DurationUnit)}>
									<SelectTrigger className="w-28">
										<SelectValue />
									</SelectTrigger>
									<SelectContent>
										{["minutes", "hours", "days"].map((v) => (
											<SelectItem key={v} value={v}>
												{copy.units[v as DurationUnit]}
											</SelectItem>
										))}
									</SelectContent>
								</Select>
							</div>
						</div>
						<div className="flex items-center justify-between">
							<Label>{copy.notifyOnce}</Label>
							<Switch
								checked={form.notify_once_per_reset_cycle}
								onCheckedChange={(v) => setForm({ ...form, notify_once_per_reset_cycle: v })}
							/>
						</div>
					</div>
					<div className="bg-muted/20 space-y-2 rounded-sm border p-4">
						<Label>{copy.cel}</Label>
						<Textarea
							className="min-h-28 font-mono text-sm"
							data-testid="alert-rule-cel"
							value={form.cel_expression}
							onChange={(e) => setForm({ ...form, cel_expression: e.target.value })}
						/>
						<p className="text-muted-foreground text-xs">{copy.celHelp}</p>
					</div>
					<div className="space-y-3 rounded-sm border p-4">
						<Label>{copy.channels}</Label>
						<div className="grid gap-2 sm:grid-cols-2">
							{channels.map((channel) => (
								<label
									key={channel.id}
									className="hover:bg-muted/50 flex cursor-pointer items-center gap-2 rounded border p-3 transition-colors duration-150"
								>
									<input
										type="checkbox"
										checked={form.channel_ids.includes(channel.id)}
										onChange={(e) =>
											setForm({
												...form,
												channel_ids: e.target.checked
													? [...form.channel_ids, channel.id]
													: form.channel_ids.filter((id) => id !== channel.id),
											})
										}
									/>
									{channel.name}
								</label>
							))}
						</div>
					</div>
					<DialogFooter className="bg-background/95 sticky bottom-0 -mx-6 border-t px-6 pb-0 backdrop-blur-sm">
						<Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
							{copy.cancel}
						</Button>
						<Button type="submit" data-testid="alert-rule-save">
							{copy.save}
						</Button>
					</DialogFooter>
				</form>
			</DialogContent>
		</Dialog>
	);
}

export function AlertRulesView() {
	const canEdit = useRbac(RbacResource.AlertRules, RbacOperation.Update);
	const { data } = useGetAlertRulesQuery();
	const { data: channels } = useGetAlertChannelsQuery();
	const { data: providers } = useGetProvidersQuery();
	const [open, setOpen] = useState(false);
	const [editing, setEditing] = useState<AlertRule | null>(null);
	const [remove] = useDeleteAlertRuleMutation();
	const [evaluate] = useEvaluateAlertRuleMutation();
	const [evaluation, setEvaluation] = useState<AlertRule | null>(null);
	const [ignoreCooldown, setIgnoreCooldown] = useState(false);
	return (
		<div>
			<Header
				title={copy.rules}
				description={copy.rulesDescription}
				action={
					canEdit ? (
						<Button
							data-testid="alert-rules-add"
							onClick={() => {
								setEditing(null);
								setOpen(true);
							}}
						>
							<Plus className="size-4" /> {copy.add}
						</Button>
					) : undefined
				}
			/>
			{!data?.rules.length ? (
				<Empty />
			) : (
				<Table>
					<TableHeader>
						<TableRow>
							<TableHead>{copy.name}</TableHead>
							<TableHead>{copy.scope}</TableHead>
							<TableHead>{copy.window}</TableHead>
							<TableHead>{copy.channels}</TableHead>
							<TableHead />
						</TableRow>
					</TableHeader>
					<TableBody>
						{data.rules.map((rule) => (
							<TableRow key={rule.id} data-testid="alert-rule-row">
								<TableCell>{rule.name}</TableCell>
								<TableCell>
									{copy.scopeLabels[rule.scope_type]}: {rule.scope_id}
									{rule.target_id ? ` / ${rule.target_id}` : ""}
								</TableCell>
								<TableCell>
									{rule.window_seconds}s / min {rule.min_requests}
								</TableCell>
								<TableCell>{rule.channel_ids.length}</TableCell>
								<TableCell className="text-right">
									{canEdit && (
										<>
											<Button
												variant="ghost"
												size="sm"
												data-testid="alert-rule-evaluate"
												onClick={() => {
													setEvaluation(rule);
													setIgnoreCooldown(false);
												}}
											>
												{copy.evaluate}
											</Button>
											<Button
												variant="ghost"
												size="icon"
												onClick={() => {
													setEditing(rule);
													setOpen(true);
												}}
											>
												<Pencil className="size-4" />
											</Button>
											<Button variant="ghost" size="icon" onClick={() => remove(rule.id)}>
												<Trash2 className="size-4" />
											</Button>
										</>
									)}
								</TableCell>
							</TableRow>
						))}
					</TableBody>
				</Table>
			)}
			<RuleDialog
				open={open}
				onOpenChange={setOpen}
				rule={editing}
				channels={channels?.channels ?? []}
				providers={(providers ?? []).map((provider) => provider.name)}
			/>
			<Dialog open={Boolean(evaluation)} onOpenChange={(v) => !v && setEvaluation(null)}>
				<DialogContent>
					<DialogHeader>
						<DialogTitle>
							{copy.evaluate} {evaluation?.name}
						</DialogTitle>
						<DialogDescription>{copy.evaluateDescription}</DialogDescription>
					</DialogHeader>
					<label className="flex items-center justify-between">
						<span>{copy.ignoreCooldown}</span>
						<Switch data-testid="alert-rule-ignore-cooldown" checked={ignoreCooldown} onCheckedChange={setIgnoreCooldown} />
					</label>
					<DialogFooter>
						<Button variant="outline" onClick={() => setEvaluation(null)}>
							{copy.cancel}
						</Button>
						<Button
							data-testid="alert-rule-evaluate-confirm"
							onClick={async () => {
								if (!evaluation) return;
								try {
									const result = await evaluate({ id: evaluation.id, ignoreCooldown }).unwrap();
									toast.success(copy.evaluationResult(result.matched_targets, result.sent_count));
									setEvaluation(null);
								} catch (e) {
									toast.error(getErrorMessage(e));
								}
							}}
						>
							{copy.evaluateButton}
						</Button>
					</DialogFooter>
				</DialogContent>
			</Dialog>
		</div>
	);
}

export function AlertHistoryView() {
	const canEdit = useRbac(RbacResource.AlertHistory, RbacOperation.Update);
	const [status, setStatus] = useState<AlertStatus | "all">("all");
	const [scope, setScope] = useState<AlertScopeType | "all">("all");
	const [channel, setChannel] = useState<"all" | "slack" | "microsoft_teams" | "wecom" | "pagerduty" | "webhook">("all");
	const [offset, setOffset] = useState(0);
	const limit = 25;
	const { data, refetch } = useGetAlertHistoryQuery({
		limit,
		offset,
		status: status === "all" ? undefined : [status],
		scope_type: scope === "all" ? undefined : [scope],
		channel_type: channel === "all" ? undefined : [channel],
	});
	const [evaluate, evaluating] = useEvaluateAlertsMutation();
	const [detail, setDetail] = useState<AlertHistoryRecord | null>(null);
	return (
		<div>
			<Header
				title={copy.history}
				description={copy.historyDescription}
				action={
					canEdit ? (
						<Button
							data-testid="alert-evaluate-all"
							disabled={evaluating.isLoading}
							onClick={async () => {
								try {
									await evaluate().unwrap();
									toast.success(copy.evaluationCompleted);
									refetch();
								} catch (e) {
									toast.error(getErrorMessage(e));
								}
							}}
						>
							<RefreshCw className="size-4" /> {copy.evaluate}
						</Button>
					) : undefined
				}
			/>
			<div className="mb-4 flex gap-2">
				{[
					[status, setStatus, ["all", "sent", "failed", "skipped"]],
					[scope, setScope, ["all", "provider", "virtual_key", "team", "customer"]],
					[channel, setChannel, ["all", "slack", "microsoft_teams", "wecom", "pagerduty", "webhook"]],
				].map(([value, setter, values], index) => (
					<Select key={index} value={value as string} onValueChange={(v) => (setter as (v: never) => void)(v as never)}>
						<SelectTrigger className="w-44" data-testid={`alert-history-filter-${index}`}>
							<SelectValue />
						</SelectTrigger>
						<SelectContent>
							{(values as string[]).map((v) => (
								<SelectItem key={v} value={v}>
									{index === 0
										? copy.statusLabels[v as keyof typeof copy.statusLabels]
										: index === 1
											? v === "all"
												? copy.allScopes
												: copy.scopeLabels[v as AlertScopeType]
											: v === "all"
												? copy.allChannels
												: v}
								</SelectItem>
							))}
						</SelectContent>
					</Select>
				))}
			</div>
			{!data?.history.length ? (
				<Empty />
			) : (
				<Table>
					<TableHeader>
						<TableRow>
							<TableHead>{copy.time}</TableHead>
							<TableHead>{copy.rule}</TableHead>
							<TableHead>{copy.scope}</TableHead>
							<TableHead>{copy.channel}</TableHead>
							<TableHead>{copy.status}</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{data.history.map((row) => (
							<TableRow key={row.id} className="cursor-pointer" data-testid="alert-history-row" onClick={() => setDetail(row)}>
								<TableCell>{new Date(row.created_at).toLocaleString()}</TableCell>
								<TableCell>{row.rule_name}</TableCell>
								<TableCell>
									{copy.scopeLabels[row.scope_type]}: {row.scope_id}
								</TableCell>
								<TableCell>{row.channel_name ?? row.channel_type ?? "-"}</TableCell>
								<TableCell>
									<Badge variant={row.status === "sent" ? "default" : row.status === "failed" ? "destructive" : "secondary"}>
										{copy.statusLabels[row.status]}
									</Badge>
								</TableCell>
							</TableRow>
						))}
					</TableBody>
				</Table>
			)}
			<div className="mt-4 flex justify-end gap-2">
				<Button variant="outline" size="sm" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - limit))}>
					<ChevronLeft className="size-4" />
				</Button>
				<span className="py-2 text-sm">
					{offset + 1}-{Math.min(offset + limit, data?.total ?? 0)} / {data?.total ?? 0}
				</span>
				<Button variant="outline" size="sm" disabled={offset + limit >= (data?.total ?? 0)} onClick={() => setOffset(offset + limit)}>
					<ChevronRight className="size-4" />
				</Button>
			</div>
			<Dialog open={Boolean(detail)} onOpenChange={(v) => !v && setDetail(null)}>
				<DialogContent>
					<DialogHeader>
						<DialogTitle>{detail?.rule_name}</DialogTitle>
						<DialogDescription>{copy.deliveryDetails}</DialogDescription>
					</DialogHeader>
					<pre className="bg-muted max-h-80 overflow-auto rounded p-3 text-xs">
						{JSON.stringify({ status: detail?.status, detail: safeHistoryDetail(detail?.status_detail), input: detail?.input }, null, 2)}
					</pre>
				</DialogContent>
			</Dialog>
		</div>
	);
}