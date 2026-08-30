import { Badge } from "@/components/ui/badge";
import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogFooter,
	AlertDialogHeader,
	AlertDialogTitle,
} from "@/components/ui/alertDialog";
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
import { ChevronLeft, ChevronRight, Pencil, Plus, RefreshCw, Search, Send, Trash2 } from "lucide-react";
import { FormEvent, ReactNode, useEffect, useState } from "react";
import { toast } from "sonner";
import { ChannelFormValue, channelRequest, safeHistoryDetail } from "./alertingModel";
import { alertingCopy } from "./copy";
import { AlertRuleDialog } from "./AlertRuleDialog";

const copy = alertingCopy();
function Header({ title, description, action }: { title: string; description: string; action?: ReactNode }) {
	return (
		<div className="mb-5 flex flex-wrap items-start justify-between gap-3">
			<div className="min-w-0">
				<h1 className="text-lg font-semibold">
					{title} <Badge variant="outline">{copy.beta}</Badge>
				</h1>
				<p className="text-muted-foreground mt-1 max-w-2xl text-sm">{description}</p>
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

function SearchBox({
	value,
	onChange,
	placeholder,
	testId,
}: {
	value: string;
	onChange: (value: string) => void;
	placeholder: string;
	testId: string;
}) {
	return (
		<div className="relative mb-4 max-w-md">
			<Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2" />
			<Input
				data-testid={testId}
				className="pl-9"
				value={value}
				onChange={(event) => onChange(event.target.value)}
				placeholder={placeholder}
			/>
		</div>
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
	const canCreate = useRbac(RbacResource.AlertChannels, RbacOperation.Create);
	const canUpdate = useRbac(RbacResource.AlertChannels, RbacOperation.Update);
	const canDelete = useRbac(RbacResource.AlertChannels, RbacOperation.Delete);
	const { data, isLoading } = useGetAlertChannelsQuery();
	const [dialog, setDialog] = useState(false);
	const [editing, setEditing] = useState<AlertChannel | null>(null);
	const [remove] = useDeleteAlertChannelMutation();
	const [update] = useUpdateAlertChannelMutation();
	const [test] = useTestAlertChannelMutation();
	const [deleteTarget, setDeleteTarget] = useState<AlertChannel | null>(null);
	const [search, setSearch] = useState("");
	const visibleChannels = (data?.channels ?? []).filter((channel) =>
		`${channel.name} ${channel.description ?? ""}`.toLowerCase().includes(search.toLowerCase()),
	);
	return (
		<div className="max-w-7xl">
			<Header
				title={copy.channels}
				description={copy.channelsDescription}
				action={
					canCreate ? (
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
				<>
					<SearchBox value={search} onChange={setSearch} placeholder={copy.channelsDescription} testId="alert-channel-search" />
					<div className="overflow-x-auto rounded-md border">
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
								{visibleChannels.map((channel) => (
									<TableRow key={channel.id} data-testid="alert-channel-row">
										<TableCell>
											<p className="font-medium">{channel.name}</p>
											<p className="text-muted-foreground max-w-72 truncate text-xs">{channel.description}</p>
										</TableCell>
										<TableCell>{channel.type}</TableCell>
										<TableCell>
											<Switch
												aria-label={`${copy.enabled}: ${channel.name}`}
												disabled={!canUpdate}
												checked={channel.enabled}
												onCheckedChange={async (enabled) => {
													try {
														await update({
															id: channel.id,
															data: { enabled },
														}).unwrap();
													} catch (error) {
														toast.error(getErrorMessage(error));
													}
												}}
											/>
										</TableCell>
										<TableCell className="text-right">
											{canUpdate || canDelete ? (
												<>
													{canUpdate ? (
														<Button
															aria-label={`${copy.test}: ${channel.name}`}
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
													) : null}
													{canUpdate ? (
														<Button
															aria-label={`${copy.edit}: ${channel.name}`}
															variant="ghost"
															size="icon"
															onClick={() => {
																setEditing(channel);
																setDialog(true);
															}}
														>
															<Pencil className="size-4" />
														</Button>
													) : null}
													{canDelete ? (
														<Button
															aria-label={`${copy.delete}: ${channel.name}`}
															variant="ghost"
															size="icon"
															onClick={() => setDeleteTarget(channel)}
														>
															<Trash2 className="size-4" />
														</Button>
													) : null}
												</>
											) : null}
										</TableCell>
									</TableRow>
								))}
							</TableBody>
						</Table>
					</div>
				</>
			)}
			<ChannelDialog open={dialog} onOpenChange={setDialog} channel={editing} />
			<AlertDialog open={Boolean(deleteTarget)} onOpenChange={(open) => !open && setDeleteTarget(null)}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>
							{copy.delete} {deleteTarget?.name}?
						</AlertDialogTitle>
						<AlertDialogDescription>{copy.channelsDescription}</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel>{copy.cancel}</AlertDialogCancel>
						<AlertDialogAction
							onClick={async () => {
								if (!deleteTarget) return;
								try {
									await remove(deleteTarget.id).unwrap();
									setDeleteTarget(null);
								} catch (error) {
									toast.error(getErrorMessage(error));
								}
							}}
						>
							{copy.delete}
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>
		</div>
	);
}

export function AlertRulesView() {
	const canCreate = useRbac(RbacResource.AlertRules, RbacOperation.Create);
	const canUpdate = useRbac(RbacResource.AlertRules, RbacOperation.Update);
	const canDelete = useRbac(RbacResource.AlertRules, RbacOperation.Delete);
	const { data } = useGetAlertRulesQuery();
	const { data: channels } = useGetAlertChannelsQuery();
	const { data: providers } = useGetProvidersQuery();
	const [open, setOpen] = useState(false);
	const [editing, setEditing] = useState<AlertRule | null>(null);
	const [remove] = useDeleteAlertRuleMutation();
	const [update] = useUpdateAlertRuleMutation();
	const [evaluate] = useEvaluateAlertRuleMutation();
	const [evaluateAll, evaluatingAll] = useEvaluateAlertsMutation();
	const [evaluation, setEvaluation] = useState<AlertRule | null>(null);
	const [deleteTarget, setDeleteTarget] = useState<AlertRule | null>(null);
	const [ignoreCooldown, setIgnoreCooldown] = useState(false);
	const [search, setSearch] = useState("");
	const channelNames = Object.fromEntries((channels?.channels ?? []).map((channel) => [channel.id, channel.name]));
	const visibleRules = (data?.rules ?? []).filter((rule) =>
		`${rule.name} ${rule.description ?? ""} ${rule.cel_expression}`.toLowerCase().includes(search.toLowerCase()),
	);
	return (
		<div className="max-w-7xl">
			<Header
				title={copy.rules}
				description={copy.rulesDescription}
				action={
					canCreate || canUpdate ? (
						<div className="flex items-center gap-2">
							{canUpdate ? (
								<Button
									variant="outline"
									data-testid="alert-evaluate-all"
									disabled={evaluatingAll.isLoading}
									onClick={async () => {
										try {
											await evaluateAll().unwrap();
											toast.success(copy.evaluationCompleted);
										} catch (error) {
											toast.error(getErrorMessage(error));
										}
									}}
								>
									<RefreshCw className={evaluatingAll.isLoading ? "size-4 animate-spin" : "size-4"} /> {copy.evaluate}
								</Button>
							) : null}
							{canCreate ? (
								<Button
									data-testid="alert-rules-add"
									onClick={() => {
										setEditing(null);
										setOpen(true);
									}}
								>
									<Plus className="size-4" /> {copy.add}
								</Button>
							) : null}
						</div>
					) : undefined
				}
			/>
			{!data?.rules.length ? (
				<Empty />
			) : (
				<>
					<SearchBox value={search} onChange={setSearch} placeholder={copy.rulesDescription} testId="alert-rule-search" />
					<div className="overflow-x-auto rounded-md border">
						<Table>
							<TableHeader>
								<TableRow>
									<TableHead>{copy.name}</TableHead>
									<TableHead>{copy.scope}</TableHead>
									<TableHead>{copy.cel}</TableHead>
									<TableHead>{copy.channels}</TableHead>
									<TableHead>{copy.status}</TableHead>
									<TableHead />
								</TableRow>
							</TableHeader>
							<TableBody>
								{visibleRules.map((rule) => (
									<TableRow key={rule.id} data-testid="alert-rule-row">
										<TableCell>
											<p className="font-medium">{rule.name}</p>
											<p className="text-muted-foreground max-w-56 truncate text-xs">{rule.description}</p>
										</TableCell>
										<TableCell>
											<p>{copy.scopeLabels[rule.scope_type]}</p>
											<p className="text-muted-foreground max-w-44 truncate text-xs">
												{rule.scope_id}
												{rule.target_id ? ` / ${rule.target_id}` : ""}
											</p>
										</TableCell>
										<TableCell>
											<code className="text-muted-foreground line-clamp-2 max-w-72 text-xs">{rule.cel_expression}</code>
										</TableCell>
										<TableCell className="max-w-52 text-sm">{rule.channel_ids.map((id) => channelNames[id] ?? id).join(", ")}</TableCell>
										<TableCell>
											<Switch
												aria-label={`${copy.enabled}: ${rule.name}`}
												data-testid={`alert-rule-toggle-${rule.id}`}
												disabled={!canUpdate}
												checked={rule.enabled}
												onCheckedChange={async (enabled) => {
													try {
														await update({
															id: rule.id,
															data: {
																name: rule.name,
																description: rule.description,
																enabled,
																scope_type: rule.scope_type,
																scope_id: rule.scope_id,
																target_type: rule.target_type,
																target_id: rule.target_id,
																cel_expression: rule.cel_expression,
																channel_ids: rule.channel_ids,
																query: rule.query,
																cooldown_milliseconds: rule.cooldown_milliseconds,
																window_seconds: rule.window_seconds,
																min_requests: rule.min_requests,
																notify_once_per_reset_cycle: rule.notify_once_per_reset_cycle,
															},
														}).unwrap();
													} catch (error) {
														toast.error(getErrorMessage(error));
													}
												}}
											/>
										</TableCell>
										<TableCell className="text-right">
											{canUpdate || canDelete ? (
												<>
													{canUpdate ? (
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
													) : null}
													{canUpdate ? (
														<Button
															aria-label={`${copy.edit}: ${rule.name}`}
															variant="ghost"
															size="icon"
															onClick={() => {
																setEditing(rule);
																setOpen(true);
															}}
														>
															<Pencil className="size-4" />
														</Button>
													) : null}
													{canDelete ? (
														<Button
															aria-label={`${copy.delete}: ${rule.name}`}
															variant="ghost"
															size="icon"
															onClick={() => setDeleteTarget(rule)}
														>
															<Trash2 className="size-4" />
														</Button>
													) : null}
												</>
											) : null}
										</TableCell>
									</TableRow>
								))}
							</TableBody>
						</Table>
					</div>
				</>
			)}
			<AlertRuleDialog
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
			<AlertDialog open={Boolean(deleteTarget)} onOpenChange={(open) => !open && setDeleteTarget(null)}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>
							{copy.delete} {deleteTarget?.name}?
						</AlertDialogTitle>
						<AlertDialogDescription>{copy.rulesDescription}</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel>{copy.cancel}</AlertDialogCancel>
						<AlertDialogAction
							onClick={async () => {
								if (!deleteTarget) return;
								try {
									await remove(deleteTarget.id).unwrap();
									setDeleteTarget(null);
								} catch (error) {
									toast.error(getErrorMessage(error));
								}
							}}
						>
							{copy.delete}
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>
		</div>
	);
}

export function AlertHistoryView() {
	const [status, setStatus] = useState<AlertStatus | "all">("all");
	const [scope, setScope] = useState<AlertScopeType | "all">("all");
	const [channel, setChannel] = useState<"all" | "slack" | "microsoft_teams" | "wecom" | "pagerduty" | "webhook">("all");
	const [offset, setOffset] = useState(0);
	const limit = 25;
	const { data } = useGetAlertHistoryQuery({
		limit,
		offset,
		status: status === "all" ? undefined : [status],
		scope_type: scope === "all" ? undefined : [scope],
		channel_type: channel === "all" ? undefined : [channel],
	});
	const [detail, setDetail] = useState<AlertHistoryRecord | null>(null);
	return (
		<div className="max-w-7xl">
			<Header title={copy.history} description={copy.historyDescription} />
			<div className="mb-4 flex flex-wrap gap-2">
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
				<div className="overflow-x-auto rounded-md border">
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
								<TableRow
									key={row.id}
									className="cursor-pointer"
									data-testid="alert-history-row"
									tabIndex={0}
									onClick={() => setDetail(row)}
									onKeyDown={(event) => {
										if (event.key === "Enter" || event.key === " ") {
											event.preventDefault();
											setDetail(row);
										}
									}}
								>
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
				</div>
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