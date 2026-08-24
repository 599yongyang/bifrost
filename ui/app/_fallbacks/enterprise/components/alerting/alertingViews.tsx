import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
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
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { CELRuleBuilder, type CELFieldDefinition, type CELOperatorDefinition } from "@/components/ui/custom/celBuilder";
import { VirtualKeySelector } from "@/components/entitySelectors/virtualKeySelector";
import { TeamSelector } from "@/components/entitySelectors/teamSelector";
import { CustomerSelector } from "@/components/entitySelectors/customerSelector";
import {
	getErrorMessage,
	useCreateAlertChannelMutation,
	useCreateAlertRuleMutation,
	useDeleteAlertChannelMutation,
	useTestAlertChannelMutation,
	useDeleteAlertRuleMutation,
	useEvaluateAlertsMutation,
	useGetAlertChannelsQuery,
	useGetAlertHistoryQuery,
	useGetAlertRulesQuery,
	useGetProvidersQuery,
	useGetModelsQuery,
	useUpdateAlertChannelMutation,
	useUpdateAlertRuleMutation,
} from "@/lib/store";
import {
	AlertChannel,
	AlertChannelRequest,
	AlertChannelType,
	AlertRule,
	AlertRuleRequest,
	AlertScopeType,
	AlertStatus,
} from "@/lib/types/alerting";
import { BellRing, Pencil, Plus, RefreshCw, Search, Send, Trash2 } from "lucide-react";
import { FormEvent, ReactNode, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import type { RuleGroupType, RuleType } from "react-querybuilder";
import FullPageLoader from "@/components/fullPageLoader";
import i18n from "@/lib/i18n";
import { alertMetricsForScope } from "./alertingRuleFields";
import {
	alertCooldownFromSeconds,
	alertCooldownToSeconds,
	alertWindowFromSeconds,
	alertWindowToSeconds,
	type AlertWindowUnit,
} from "./alertingDuration";
import { AlertingDurationInput } from "./alertingDurationInput";

const tr = (key: string, options?: Record<string, unknown>) => i18n.t(`workspace.alerting.${key}`, options);

const channelLabels: Record<AlertChannelType, string> = {
	slack: "Slack",
	microsoft_teams: "Microsoft Teams",
	wecom: "企业微信",
	pagerduty: "PagerDuty",
	webhook: "Webhook",
};
const scopeLabels: Record<AlertScopeType, string> = {
	virtual_key: "Virtual Key",
	team: "Team",
	customer: "Customer",
	provider: "Provider",
};

function PageHeader({ title, description, action }: { title: string; description: string; action?: ReactNode }) {
	return (
		<div className="mb-5 flex items-center justify-between">
			<div>
				<div className="flex items-center gap-2">
					<h1 className="text-foreground text-lg font-semibold">{title}</h1>
					<Badge variant="outline" className="border-emerald-500/40 text-emerald-700">
						Beta
					</Badge>
				</div>
				<p className="text-muted-foreground text-sm">{description}</p>
			</div>
			{action}
		</div>
	);
}

function EmptyState({ title, body, onAdd }: { title: string; body: string; onAdd?: () => void }) {
	return (
		<div className="flex min-h-[420px] flex-col items-center justify-center rounded-lg border border-dashed text-center">
			<div className="bg-muted mb-4 rounded-full p-3">
				<BellRing className="text-muted-foreground h-6 w-6" />
			</div>
			<h2 className="font-medium">{title}</h2>
			<p className="text-muted-foreground mt-1 max-w-md text-sm">{body}</p>
			{onAdd && (
				<Button className="mt-4 gap-2" onClick={onAdd}>
					<Plus className="h-4 w-4" /> Create one
				</Button>
			)}
		</div>
	);
}

function LoadError({ error, onRetry }: { error: unknown; onRetry: () => void }) {
	return (
		<div className="border-destructive/30 flex min-h-[320px] flex-col items-center justify-center rounded-lg border">
			<p className="font-medium">{tr("loadError")}</p>
			<p className="text-muted-foreground mt-1 text-sm">{getErrorMessage(error)}</p>
			<Button className="mt-4" variant="outline" onClick={onRetry}>
				{tr("tryAgain")}
			</Button>
		</div>
	);
}

function ConfirmDelete({
	name,
	open,
	onOpenChange,
	onConfirm,
	isLoading,
}: {
	name: string;
	open: boolean;
	onOpenChange: (open: boolean) => void;
	onConfirm: () => Promise<void>;
	isLoading: boolean;
}) {
	return (
		<AlertDialog open={open} onOpenChange={onOpenChange}>
			<AlertDialogContent>
				<AlertDialogHeader>
					<AlertDialogTitle>Delete {name}?</AlertDialogTitle>
					<AlertDialogDescription>
						This action cannot be undone. Attached rules may be disabled when their last channel is removed.
					</AlertDialogDescription>
				</AlertDialogHeader>
				<AlertDialogFooter>
					<AlertDialogCancel data-testid="alert-delete-cancel">{tr("cancel")}</AlertDialogCancel>
					<AlertDialogAction
						data-testid="alert-delete-confirm"
						disabled={isLoading}
						onClick={(event) => {
							event.preventDefault();
							void onConfirm();
						}}
					>
						{tr("delete")}
					</AlertDialogAction>
				</AlertDialogFooter>
			</AlertDialogContent>
		</AlertDialog>
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
			<Search className="text-muted-foreground absolute top-2.5 left-3 h-4 w-4" />
			<Input
				data-testid={testId}
				value={value}
				onChange={(event) => onChange(event.target.value)}
				placeholder={placeholder}
				className="pl-9"
			/>
		</div>
	);
}

type ChannelForm = {
	name: string;
	description: string;
	type: AlertChannelType;
	destination: string;
	headers: string;
	enabled: boolean;
};
const emptyChannel: ChannelForm = {
	name: "",
	description: "",
	type: "slack",
	destination: "",
	headers: "{}",
	enabled: true,
};

function ChannelDialog({
	open,
	onOpenChange,
	channel,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	channel: AlertChannel | null;
}) {
	const [form, setForm] = useState<ChannelForm>(emptyChannel);
	const [createChannel, createState] = useCreateAlertChannelMutation();
	const [updateChannel, updateState] = useUpdateAlertChannelMutation();
	useEffect(() => {
		if (!open) return;
		const key = channel?.type === "pagerduty" ? "routing_key" : channel?.type === "webhook" ? "url" : "webhook_url";
		setForm(
			channel
				? {
						name: channel.name,
						description: channel.description ?? "",
						type: channel.type,
						destination: typeof channel.config[key] === "string" ? String(channel.config[key]) : "",
						headers: JSON.stringify(channel.config.headers ?? {}, null, 2),
						enabled: channel.enabled,
					}
				: emptyChannel,
		);
	}, [channel, open]);
	const submit = async (event: FormEvent) => {
		event.preventDefault();
		try {
			const key = form.type === "pagerduty" ? "routing_key" : form.type === "webhook" ? "url" : "webhook_url";
			const config: Record<string, unknown> = { [key]: form.destination };
			if (form.type === "webhook") config.headers = JSON.parse(form.headers || "{}");
			const request: AlertChannelRequest = {
				name: form.name.trim(),
				description: form.description.trim(),
				type: form.type,
				config,
				enabled: form.enabled,
			};
			if (channel) await updateChannel({ id: channel.id, data: request }).unwrap();
			else await createChannel(request).unwrap();
			toast.success(channel ? "Alert channel updated" : "Alert channel created");
			onOpenChange(false);
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	};
	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className="sm:max-w-2xl">
				<form onSubmit={submit}>
					<DialogHeader>
						<DialogTitle>{channel ? tr("editChannel") : tr("createChannel")}</DialogTitle>
						<DialogDescription>Choose where matching alert rules deliver notifications.</DialogDescription>
					</DialogHeader>
					<div className="grid gap-4">
						<div className="grid gap-2">
							<Label htmlFor="alert-channel-name">{tr("name")}</Label>
							<Input
								id="alert-channel-name"
								data-testid="alert-channel-name"
								required
								value={form.name}
								onChange={(e) => setForm({ ...form, name: e.target.value })}
							/>
						</div>
						<div className="grid gap-2">
							<Label>{tr("description")}</Label>
							<Textarea
								data-testid="alert-channel-description"
								value={form.description}
								onChange={(e) => setForm({ ...form, description: e.target.value })}
							/>
						</div>
						<div className="grid gap-2">
							<Label>{tr("channelType")}</Label>
							<Select value={form.type} onValueChange={(type) => setForm({ ...form, type: type as AlertChannelType, destination: "" })}>
								<SelectTrigger className="w-full" data-testid="alert-channel-type">
									<SelectValue />
								</SelectTrigger>
								<SelectContent>
									{Object.entries(channelLabels).map(([value, label]) => (
										<SelectItem key={value} value={value}>
											{label}
										</SelectItem>
									))}
								</SelectContent>
							</Select>
						</div>
						<div className="grid gap-2">
							<Label>{form.type === "pagerduty" ? tr("routingKey") : tr("webhookUrl")}</Label>
							<Input
								data-testid="alert-channel-destination"
								required
								type={form.type === "pagerduty" ? "password" : "url"}
								placeholder={
									form.type === "pagerduty"
										? "PagerDuty Events API v2 key"
										: form.type === "wecom"
											? "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=..."
											: "https://..."
								}
								value={form.destination}
								onChange={(e) => setForm({ ...form, destination: e.target.value })}
							/>
						</div>
						{form.type === "wecom" && (
							<p className="text-muted-foreground -mt-2 text-xs">
								粘贴企业微信群机器人 Webhook 地址。通知内容会根据规则、供应商、模型和实时统计结果自动生成。
							</p>
						)}
						{form.type === "webhook" && (
							<div className="grid gap-2">
								<Label>{tr("customHeaders")}</Label>
								<Textarea
									data-testid="alert-channel-headers"
									className="font-mono text-xs"
									value={form.headers}
									onChange={(e) => setForm({ ...form, headers: e.target.value })}
								/>
							</div>
						)}
						<div className="flex items-center justify-between rounded-md border p-3">
							<div>
								<Label>{tr("enabled")}</Label>
								<p className="text-muted-foreground text-xs">Allow rules to deliver through this channel.</p>
							</div>
							<Switch
								data-testid="alert-channel-enabled"
								checked={form.enabled}
								onCheckedChange={(enabled) => setForm({ ...form, enabled })}
							/>
						</div>
					</div>
					<DialogFooter className="mt-5">
						<Button data-testid="alert-channel-cancel" type="button" variant="outline" onClick={() => onOpenChange(false)}>
							Cancel
						</Button>
						<Button data-testid="alert-channel-submit" disabled={createState.isLoading || updateState.isLoading} type="submit">
							{channel ? tr("saveChanges") : tr("createChannel")}
						</Button>
					</DialogFooter>
				</form>
			</DialogContent>
		</Dialog>
	);
}

export function AlertChannelsViewImpl() {
	const canCreate = useRbac(RbacResource.AlertChannels, RbacOperation.Create);
	const canUpdate = useRbac(RbacResource.AlertChannels, RbacOperation.Update);
	const canDelete = useRbac(RbacResource.AlertChannels, RbacOperation.Delete);
	const { data, isLoading, isError, error, refetch } = useGetAlertChannelsQuery(undefined, {
		pollingInterval: 10000,
	});
	const [updateChannel] = useUpdateAlertChannelMutation();
	const [deleteChannel, deleteState] = useDeleteAlertChannelMutation();
	const [testChannel, testState] = useTestAlertChannelMutation();
	const [testingChannelID, setTestingChannelID] = useState<string | null>(null);
	const [search, setSearch] = useState("");
	const [dialogOpen, setDialogOpen] = useState(false);
	const [editing, setEditing] = useState<AlertChannel | null>(null);
	const [deleteTarget, setDeleteTarget] = useState<AlertChannel | null>(null);
	const channels = useMemo(
		() => (data?.channels ?? []).filter((item) => `${item.name} ${item.description ?? ""}`.toLowerCase().includes(search.toLowerCase())),
		[data, search],
	);
	const openCreate = () => {
		setEditing(null);
		setDialogOpen(true);
	};
	if (isLoading && !data) return <FullPageLoader />;
	if (isError) return <LoadError error={error} onRetry={refetch} />;
	return (
		<div className="flex h-full flex-col overflow-auto">
			<PageHeader
				title={tr("channelsTitle")}
				description={tr("channelsDescription")}
				action={
					<div className="flex gap-2">
						<Button data-testid="alert-channel-refresh" variant="outline" size="icon" onClick={() => refetch()}>
							<RefreshCw className="h-4 w-4" />
						</Button>
						{canCreate && (
							<Button data-testid="add-alert-channel" onClick={openCreate} className="gap-2">
								<Plus className="h-4 w-4" /> {tr("addChannel")}
							</Button>
						)}
					</div>
				}
			/>
			{!isLoading && (data?.count ?? 0) === 0 ? (
				<EmptyState title={tr("noChannels")} body={tr("noChannelsDescription")} onAdd={openCreate} />
			) : (
				<>
					<SearchBox value={search} onChange={setSearch} placeholder={tr("searchChannels")} testId="alert-channel-search" />
					<div className="rounded-md border">
						<Table>
							<TableHeader>
								<TableRow>
									<TableHead>{tr("name")}</TableHead>
									<TableHead>{tr("type")}</TableHead>
									<TableHead>{tr("status")}</TableHead>
									<TableHead className="w-52" />
								</TableRow>
							</TableHeader>
							<TableBody>
								{channels.map((item) => (
									<TableRow key={item.id} data-testid={`alert-channel-row-${item.id}`}>
										<TableCell>
											<div className="font-medium">{item.name}</div>
											<div className="text-muted-foreground text-xs">{item.description}</div>
										</TableCell>
										<TableCell>{channelLabels[item.type]}</TableCell>
										<TableCell>
											<Switch
												data-testid={`alert-channel-toggle-${item.id}`}
												disabled={!canUpdate}
												checked={item.enabled}
												onCheckedChange={async (enabled) => {
													try {
														await updateChannel({
															id: item.id,
															data: { ...item, enabled },
														}).unwrap();
													} catch (error) {
														toast.error(getErrorMessage(error));
													}
												}}
											/>
										</TableCell>
										<TableCell>
											<div className="flex items-center justify-end gap-1">
												{canUpdate && (
													<Button
														data-testid={`alert-channel-test-${item.id}`}
														variant="outline"
														size="sm"
														className="gap-1.5"
														disabled={testState.isLoading}
														onClick={async () => {
															setTestingChannelID(item.id);
															try {
																await testChannel(item.id).unwrap();
																toast.success(tr("testNotificationSent"));
															} catch (error) {
																toast.error(getErrorMessage(error));
															} finally {
																setTestingChannelID(null);
															}
														}}
													>
														<Send className="h-3.5 w-3.5" />
														{testingChannelID === item.id ? tr("sendingTest") : tr("sendTest")}
													</Button>
												)}
												{canUpdate && (
													<Button
														data-testid={`alert-channel-edit-${item.id}`}
														variant="ghost"
														size="icon"
														onClick={() => {
															setEditing(item);
															setDialogOpen(true);
														}}
													>
														<Pencil className="h-4 w-4" />
													</Button>
												)}
												{canDelete && (
													<Button
														data-testid={`alert-channel-delete-${item.id}`}
														variant="ghost"
														size="icon"
														onClick={() => setDeleteTarget(item)}
													>
														<Trash2 className="text-destructive h-4 w-4" />
													</Button>
												)}
											</div>
										</TableCell>
									</TableRow>
								))}
							</TableBody>
						</Table>
					</div>
				</>
			)}
			<ChannelDialog open={dialogOpen} onOpenChange={setDialogOpen} channel={editing} />
			<ConfirmDelete
				name={deleteTarget?.name ?? "channel"}
				open={!!deleteTarget}
				onOpenChange={(open) => !open && setDeleteTarget(null)}
				isLoading={deleteState.isLoading}
				onConfirm={async () => {
					if (!deleteTarget) return;
					try {
						await deleteChannel(deleteTarget.id).unwrap();
						toast.success("Alert channel deleted");
						setDeleteTarget(null);
					} catch (error) {
						toast.error(getErrorMessage(error));
					}
				}}
			/>
		</div>
	);
}

type RuleForm = {
	name: string;
	description: string;
	scopeType: AlertScopeType;
	scopeID: string;
	targetID: string;
	channelIDs: string[];
	expression: string;
	cooldownValue: string;
	cooldownUnit: AlertWindowUnit;
	windowValue: string;
	windowUnit: AlertWindowUnit;
	minRequests: string;
	notifyOncePerResetCycle: boolean;
	query: RuleGroupType;
	enabled: boolean;
};
const emptyRule: RuleForm = {
	name: "",
	description: "",
	scopeType: "virtual_key",
	scopeID: "",
	targetID: "",
	channelIDs: [],
	expression: "budget_usage_percent >= 80.0",
	cooldownValue: "1",
	cooldownUnit: "minutes",
	windowValue: "5",
	windowUnit: "minutes",
	minRequests: "100",
	notifyOncePerResetCycle: false,
	query: {
		combinator: "and",
		rules: [{ field: "budget_usage_percent", operator: ">=", value: 80 }],
	},
	enabled: true,
};
const alertOperators: CELOperatorDefinition[] = [
	{ name: ">", label: "greater than", celSyntax: ">" },
	{ name: ">=", label: "greater than or equal", celSyntax: ">=" },
	{ name: "<", label: "less than", celSyntax: "<" },
	{ name: "<=", label: "less than or equal", celSyntax: "<=" },
	{ name: "==", label: "equals", celSyntax: "==" },
	{ name: "!=", label: "does not equal", celSyntax: "!=" },
];

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

function RuleDialog({ open, onOpenChange, rule }: { open: boolean; onOpenChange: (open: boolean) => void; rule: AlertRule | null }) {
	const [form, setForm] = useState<RuleForm>(emptyRule);
	const { data: channelsData } = useGetAlertChannelsQuery();
	const { data: providers = [] } = useGetProvidersQuery();
	const { data: providerModels } = useGetModelsQuery(
		{ provider: form.scopeID, unfiltered: true, limit: 1000 },
		{ skip: form.scopeType !== "provider" || !form.scopeID },
	);
	const [createRule, createState] = useCreateAlertRuleMutation();
	const [updateRule, updateState] = useUpdateAlertRuleMutation();
	const metrics = alertMetricsForScope(form.scopeType).map(([name, labelKey]) => [name, tr(labelKey)]);
	const builderFields: CELFieldDefinition[] = metrics.map(([name, label]) => ({
		name,
		label,
		inputType: "number",
		operators: alertOperators.map((operator) => operator.name),
		defaultOperator: ">=",
		defaultValue: 0,
	}));
	const setRuleKind = (provider: boolean) => {
		setForm((current) => ({
			...current,
			scopeType: provider ? "provider" : "virtual_key",
			scopeID: "",
			targetID: "",
			expression: provider ? "provider_error_rate >= 10.0" : "budget_usage_percent >= 80.0",
			query: {
				combinator: "and",
				rules: [{ field: provider ? "provider_error_rate" : "budget_usage_percent", operator: ">=", value: provider ? 10 : 80 }],
			},
		}));
	};
	useEffect(() => {
		if (open)
			setForm(
				rule
					? {
							name: rule.name,
							description: rule.description ?? "",
							scopeType: rule.scope_type,
							scopeID: rule.scope_id,
							targetID: rule.target_id ?? "",
							channelIDs: rule.channel_ids,
							expression: rule.cel_expression,
							...alertCooldownFromSeconds(rule.cooldown_milliseconds / 1000),
							...alertWindowFromSeconds(rule.window_seconds || 300),
							minRequests: String(rule.min_requests || 1),
							notifyOncePerResetCycle: rule.notify_once_per_reset_cycle ?? false,
							query: (rule.query as RuleGroupType | undefined) ?? { combinator: "and", rules: [] },
							enabled: rule.enabled,
						}
					: emptyRule,
			);
	}, [open, rule]);
	const submit = async (event: FormEvent) => {
		event.preventDefault();
		try {
			const request: AlertRuleRequest = {
				name: form.name.trim(),
				description: form.description.trim(),
				enabled: form.enabled,
				scope_type: form.scopeType,
				scope_id: form.scopeID.trim(),
				cel_expression: form.expression.trim(),
				channel_ids: form.channelIDs,
				cooldown_milliseconds: alertCooldownToSeconds(form.cooldownValue, form.cooldownUnit) * 1000,
				window_seconds: alertWindowToSeconds(form.windowValue, form.windowUnit),
				min_requests: Math.max(1, Number(form.minRequests) || 1),
				notify_once_per_reset_cycle: form.scopeType === "provider" ? false : form.notifyOncePerResetCycle,
				query: form.query as unknown as Record<string, unknown>,
				...(form.targetID.trim()
					? {
							target_type: form.scopeType === "provider" ? ("model" as const) : ("budget" as const),
							target_id: form.targetID.trim(),
						}
					: {}),
			};
			if (rule) await updateRule({ id: rule.id, data: request }).unwrap();
			else await createRule(request).unwrap();
			toast.success(rule ? "Alert rule updated" : "Alert rule created");
			onOpenChange(false);
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	};
	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className="w-full overflow-x-hidden sm:max-w-4xl">
				<form onSubmit={submit} className="w-full max-w-full min-w-0">
					<DialogHeader>
						<DialogTitle>{rule ? tr("editRule") : tr("createRule")}</DialogTitle>
						<DialogDescription>Monitor governance usage or provider reliability and notify selected channels.</DialogDescription>
					</DialogHeader>
					<div className="grid min-w-0 gap-4">
						<div className="grid min-w-0 gap-2 sm:grid-cols-2" data-testid="alert-rule-kind-selector">
							<Button
								type="button"
								variant={form.scopeType === "provider" ? "outline" : "default"}
								className="h-auto min-w-0 justify-start p-3 text-left whitespace-normal"
								onClick={() => setRuleKind(false)}
								data-testid="alert-rule-kind-governance"
							>
								<div className="min-w-0">
									<div className="font-medium">{tr("governanceRuleType")}</div>
									<div className="mt-0.5 text-xs opacity-80">{tr("governanceRuleTypeDescription")}</div>
								</div>
							</Button>
							<Button
								type="button"
								variant={form.scopeType === "provider" ? "default" : "outline"}
								className="h-auto min-w-0 justify-start p-3 text-left whitespace-normal"
								onClick={() => setRuleKind(true)}
								data-testid="alert-rule-kind-provider-failures"
							>
								<div className="min-w-0">
									<div className="font-medium">{tr("providerFailureRuleType")}</div>
									<div className="mt-0.5 text-xs opacity-80">{tr("providerFailureRuleTypeDescription")}</div>
								</div>
							</Button>
						</div>
						<div className="grid gap-2">
							<Label>{tr("name")}</Label>
							<Input
								data-testid="alert-rule-name"
								required
								value={form.name}
								onChange={(e) => setForm({ ...form, name: e.target.value })}
							/>
						</div>
						<div className="grid gap-2">
							<Label>{tr("description")}</Label>
							<Textarea
								data-testid="alert-rule-description"
								value={form.description}
								onChange={(e) => setForm({ ...form, description: e.target.value })}
							/>
						</div>
						<div className="grid gap-4 sm:grid-cols-2">
							<div className="grid gap-2">
								<Label>{tr("scopeType")}</Label>
								<Select
									value={form.scopeType}
									onValueChange={(value) => {
										const provider = value === "provider";
										setForm({
											...form,
											scopeType: value as AlertScopeType,
											targetID: "",
											expression: provider ? "provider_error_rate >= 10.0" : "budget_usage_percent >= 80.0",
											query: {
												combinator: "and",
												rules: [
													{
														field: provider ? "provider_error_rate" : "budget_usage_percent",
														operator: ">=",
														value: provider ? 10 : 80,
													},
												],
											},
										});
									}}
								>
									<SelectTrigger className="w-full" data-testid="alert-rule-scope-type">
										<SelectValue />
									</SelectTrigger>
									<SelectContent>
										{Object.entries(scopeLabels).map(([value, label]) => (
											<SelectItem key={value} value={value}>
												{label}
											</SelectItem>
										))}
									</SelectContent>
								</Select>
							</div>
							<div className="grid gap-2" data-testid="alert-rule-scope-value">
								<Label>{form.scopeType === "provider" ? tr("provider") : tr("scopeId")}</Label>
								{form.scopeType === "provider" && (
									<Select value={form.scopeID} onValueChange={(scopeID) => setForm({ ...form, scopeID })}>
										<SelectTrigger className="w-full" data-testid="alert-rule-provider-select">
											<SelectValue placeholder="Select provider" />
										</SelectTrigger>
										<SelectContent>
											{providers.map((provider) => (
												<SelectItem key={provider.name} value={provider.name}>
													{provider.name}
												</SelectItem>
											))}
										</SelectContent>
									</Select>
								)}
								{form.scopeType === "virtual_key" && (
									<VirtualKeySelector value={form.scopeID} onChange={(scopeID) => setForm({ ...form, scopeID })} />
								)}
								{form.scopeType === "team" && <TeamSelector value={form.scopeID} onChange={(scopeID) => setForm({ ...form, scopeID })} />}
								{form.scopeType === "customer" && (
									<CustomerSelector value={form.scopeID} onChange={(scopeID) => setForm({ ...form, scopeID })} />
								)}
							</div>
						</div>
						<div className={form.scopeType === "provider" ? "grid gap-4" : "grid gap-4 sm:grid-cols-2"}>
							<div className="grid gap-2">
								<Label>{form.scopeType === "provider" ? "Model (optional)" : "Specific budget ID (optional)"}</Label>
								{form.scopeType === "provider" ? (
									<Select
										value={form.targetID || "__all__"}
										onValueChange={(value) => setForm({ ...form, targetID: value === "__all__" ? "" : value })}
									>
										<SelectTrigger className="w-full" data-testid="alert-rule-model-select">
											<SelectValue placeholder="All models" />
										</SelectTrigger>
										<SelectContent>
											<SelectItem value="__all__">All models</SelectItem>
											{(providerModels?.models ?? []).map((model) => (
												<SelectItem key={model.name} value={model.name}>
													{model.name}
												</SelectItem>
											))}
										</SelectContent>
									</Select>
								) : (
									<Input
										data-testid="alert-rule-budget-id"
										placeholder="Any budget"
										value={form.targetID}
										onChange={(e) => setForm({ ...form, targetID: e.target.value })}
									/>
								)}
							</div>
							{form.scopeType !== "provider" && (
								<AlertingDurationInput
									label={tr("cooldown")}
									value={form.cooldownValue}
									unit={form.cooldownUnit}
									onChange={(cooldownValue, cooldownUnit) => setForm({ ...form, cooldownValue, cooldownUnit })}
									allowZero
									testIdPrefix="alert-rule-cooldown"
									description={tr("ruleCooldownDescription")}
								/>
							)}
						</div>
						{form.scopeType === "provider" && (
							<div className="overflow-hidden rounded-md border" data-testid="alert-rule-noise-controls">
								<div className="border-b px-4 py-3.5">
									<div className="font-medium">{tr("statisticsAndNoise")}</div>
									<p className="text-muted-foreground mt-0.5 text-xs">{tr("statisticsAndNoiseDescription")}</p>
								</div>
								<div className="grid gap-5 p-4 sm:grid-cols-3 sm:gap-0">
									<div className="grid min-w-0 content-start gap-2 sm:pr-4">
										<Label>{tr("rollingWindow")}</Label>
										<AlertingDurationInput
											value={form.windowValue}
											unit={form.windowUnit}
											onChange={(windowValue, windowUnit) => setForm({ ...form, windowValue, windowUnit })}
											testIdPrefix="alert-rule-window"
										/>
										<p className="text-muted-foreground text-xs leading-5">{tr("rollingWindowDescription")}</p>
									</div>
									<div className="grid min-w-0 content-start gap-2 border-t pt-5 sm:border-t-0 sm:border-l sm:px-4 sm:pt-0">
										<Label>{tr("minimumSampleSize")}</Label>
										<Input
											data-testid="alert-rule-minimum-samples"
											min="1"
											step="1"
											type="number"
											required
											value={form.minRequests}
											onChange={(event) => setForm({ ...form, minRequests: event.target.value })}
										/>
										<p className="text-muted-foreground text-xs leading-5">{tr("minimumSampleDescription")}</p>
									</div>
									<div className="grid min-w-0 content-start gap-2 border-t pt-5 sm:border-t-0 sm:border-l sm:pt-0 sm:pr-0 sm:pl-4">
										<Label>{tr("notificationCooldown")}</Label>
										<AlertingDurationInput
											value={form.cooldownValue}
											unit={form.cooldownUnit}
											onChange={(cooldownValue, cooldownUnit) => setForm({ ...form, cooldownValue, cooldownUnit })}
											allowZero
											testIdPrefix="alert-rule-cooldown"
										/>
										<p className="text-muted-foreground text-xs leading-5">{tr("notificationCooldownDescription")}</p>
									</div>
								</div>
							</div>
						)}
						{form.scopeType !== "provider" && (
							<div className="flex items-center justify-between rounded-md border p-3">
								<div>
									<Label>{tr("notifyOnce")}</Label>
									<p className="text-muted-foreground text-xs">
										Suppress additional sends until the matched budget or rate-limit counter resets.
									</p>
								</div>
								<Switch
									checked={form.notifyOncePerResetCycle}
									onCheckedChange={(notifyOncePerResetCycle) => setForm({ ...form, notifyOncePerResetCycle })}
								/>
							</div>
						)}
						<div className="grid gap-2">
							<Label>{tr("channels")}</Label>
							<div className="grid gap-2 rounded-md border p-3 sm:grid-cols-2">
								{(channelsData?.channels ?? []).map((item) => (
									<label key={item.id} className="flex cursor-pointer items-center gap-2 text-sm">
										<input
											type="checkbox"
											checked={form.channelIDs.includes(item.id)}
											onChange={(e) =>
												setForm({
													...form,
													channelIDs: e.target.checked ? [...form.channelIDs, item.id] : form.channelIDs.filter((id) => id !== item.id),
												})
											}
										/>
										{item.name}
									</label>
								))}
							</div>
						</div>
						<div className="space-y-2 rounded-md border p-4" data-testid="alert-rule-condition-builder">
							<Label>{tr("condition")}</Label>
							<CELRuleBuilder
								key={`${rule?.id ?? "new"}-${form.scopeType}`}
								fields={builderFields}
								operators={alertOperators}
								convertToCEL={alertQueryToCEL}
								initialQuery={form.query}
								initialCel={form.expression}
								initialMode={rule && !rule.query ? "cel" : "builder"}
								allowCelMode
								onChange={(expression, query) => setForm((current) => ({ ...current, expression, query }))}
							/>
						</div>
						<div className="flex items-center justify-between rounded-md border p-3">
							<div>
								<Label>{tr("enabled")}</Label>
								<p className="text-muted-foreground text-xs">{tr("evaluateEachSweep")}</p>
							</div>
							<Switch checked={form.enabled} onCheckedChange={(enabled) => setForm({ ...form, enabled })} />
						</div>
					</div>
					<DialogFooter className="mt-5">
						<Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
							Cancel
						</Button>
						<Button disabled={createState.isLoading || updateState.isLoading || form.channelIDs.length === 0} type="submit">
							{rule ? tr("saveChanges") : tr("createRule")}
						</Button>
					</DialogFooter>
				</form>
			</DialogContent>
		</Dialog>
	);
}

export function AlertRulesViewImpl() {
	const canCreate = useRbac(RbacResource.AlertRules, RbacOperation.Create);
	const canUpdate = useRbac(RbacResource.AlertRules, RbacOperation.Update);
	const canDelete = useRbac(RbacResource.AlertRules, RbacOperation.Delete);
	const { data, isLoading, isError, error, refetch } = useGetAlertRulesQuery(undefined, {
		pollingInterval: 10000,
	});
	const { data: channelsData } = useGetAlertChannelsQuery();
	const [updateRule] = useUpdateAlertRuleMutation();
	const [deleteRule, deleteState] = useDeleteAlertRuleMutation();
	const [search, setSearch] = useState("");
	const [dialogOpen, setDialogOpen] = useState(false);
	const [editing, setEditing] = useState<AlertRule | null>(null);
	const [deleteTarget, setDeleteTarget] = useState<AlertRule | null>(null);
	const channelNames = useMemo(
		() => Object.fromEntries((channelsData?.channels ?? []).map((item) => [item.id, item.name])),
		[channelsData],
	);
	const rules = useMemo(
		() => (data?.rules ?? []).filter((item) => `${item.name} ${item.cel_expression}`.toLowerCase().includes(search.toLowerCase())),
		[data, search],
	);
	const openCreate = () => {
		setEditing(null);
		setDialogOpen(true);
	};
	if (isLoading && !data) return <FullPageLoader />;
	if (isError) return <LoadError error={error} onRetry={refetch} />;
	return (
		<div className="flex h-full flex-col overflow-auto">
			<PageHeader
				title={tr("rulesTitle")}
				description={tr("rulesDescription")}
				action={
					<div className="flex gap-2">
						<Button data-testid="alert-rule-refresh" variant="outline" size="icon" onClick={() => refetch()}>
							<RefreshCw className="h-4 w-4" />
						</Button>
						{canCreate && (
							<Button data-testid="add-alert-rule" onClick={openCreate} className="gap-2">
								<Plus className="h-4 w-4" /> {tr("addRule")}
							</Button>
						)}
					</div>
				}
			/>
			{!isLoading && (data?.count ?? 0) === 0 ? (
				<EmptyState title={tr("noRules")} body={tr("noRulesDescription")} onAdd={openCreate} />
			) : (
				<>
					<SearchBox value={search} onChange={setSearch} placeholder={tr("searchRules")} testId="alert-rule-search" />
					<div className="rounded-md border">
						<Table>
							<TableHeader>
								<TableRow>
									<TableHead>{tr("name")}</TableHead>
									<TableHead>{tr("scope")}</TableHead>
									<TableHead>{tr("condition")}</TableHead>
									<TableHead>{tr("channels")}</TableHead>
									<TableHead>{tr("status")}</TableHead>
									<TableHead className="w-28" />
								</TableRow>
							</TableHeader>
							<TableBody>
								{rules.map((item) => (
									<TableRow key={item.id} data-testid={`alert-rule-row-${item.id}`}>
										<TableCell>
											<div className="font-medium">{item.name}</div>
											<div className="text-muted-foreground text-xs">{item.description}</div>
										</TableCell>
										<TableCell>
											{scopeLabels[item.scope_type]}
											<div className="text-muted-foreground max-w-40 truncate text-xs">{item.scope_id}</div>
										</TableCell>
										<TableCell>
											<code className="text-muted-foreground text-xs">{item.cel_expression}</code>
										</TableCell>
										<TableCell>{item.channel_ids.map((id) => channelNames[id] ?? id).join(", ")}</TableCell>
										<TableCell>
											<Switch
												data-testid={`alert-rule-toggle-${item.id}`}
												disabled={!canUpdate}
												checked={item.enabled}
												onCheckedChange={async (enabled) => {
													try {
														await updateRule({ id: item.id, data: { ...item, enabled } }).unwrap();
													} catch (error) {
														toast.error(getErrorMessage(error));
													}
												}}
											/>
										</TableCell>
										<TableCell>
											<div className="flex">
												{canUpdate && (
													<Button
														data-testid={`alert-rule-edit-${item.id}`}
														variant="ghost"
														size="icon"
														onClick={() => {
															setEditing(item);
															setDialogOpen(true);
														}}
													>
														<Pencil className="h-4 w-4" />
													</Button>
												)}
												{canDelete && (
													<Button
														data-testid={`alert-rule-delete-${item.id}`}
														variant="ghost"
														size="icon"
														onClick={() => setDeleteTarget(item)}
													>
														<Trash2 className="text-destructive h-4 w-4" />
													</Button>
												)}
											</div>
										</TableCell>
									</TableRow>
								))}
							</TableBody>
						</Table>
					</div>
				</>
			)}
			<RuleDialog open={dialogOpen} onOpenChange={setDialogOpen} rule={editing} />
			<ConfirmDelete
				name={deleteTarget?.name ?? "rule"}
				open={!!deleteTarget}
				onOpenChange={(open) => !open && setDeleteTarget(null)}
				isLoading={deleteState.isLoading}
				onConfirm={async () => {
					if (!deleteTarget) return;
					try {
						await deleteRule(deleteTarget.id).unwrap();
						toast.success("Alert rule deleted");
						setDeleteTarget(null);
					} catch (error) {
						toast.error(getErrorMessage(error));
					}
				}}
			/>
		</div>
	);
}

function statusVariant(status: AlertStatus): "outline" | "destructive" | "secondary" {
	return status === "failed" ? "destructive" : status === "skipped" ? "secondary" : "outline";
}
export function AlertHistoryViewImpl() {
	const canEvaluate = useRbac(RbacResource.AlertRules, RbacOperation.Update);
	const [status, setStatus] = useState<"all" | AlertStatus>("all");
	const [scopeType, setScopeType] = useState<"all" | AlertScopeType>("all");
	const [channelType, setChannelType] = useState<"all" | AlertChannelType>("all");
	const [offset, setOffset] = useState(0);
	const limit = 25;
	const { data, isLoading, isError, error, refetch } = useGetAlertHistoryQuery(
		{
			limit,
			offset,
			...(status !== "all" ? { status: [status] } : {}),
			...(scopeType !== "all" ? { scope_type: [scopeType] } : {}),
			...(channelType !== "all" ? { channel_type: [channelType] } : {}),
		},
		{ pollingInterval: 10000 },
	);
	const [evaluate, evaluateState] = useEvaluateAlertsMutation();
	const rows = data?.history ?? [];
	const total = data?.total ?? 0;
	if (isLoading && !data) return <FullPageLoader />;
	if (isError) return <LoadError error={error} onRetry={refetch} />;
	return (
		<div className="flex h-full flex-col overflow-auto">
			<PageHeader
				title={tr("historyTitle")}
				description={tr("historyDescription")}
				action={
					<div className="flex gap-2">
						<Button data-testid="alert-history-refresh" variant="outline" onClick={() => refetch()} className="gap-2">
							<RefreshCw className="h-4 w-4" /> {tr("refresh")}
						</Button>
						{canEvaluate && (
							<Button
								data-testid="alert-history-evaluate"
								disabled={evaluateState.isLoading}
								onClick={async () => {
									try {
										await evaluate().unwrap();
										await refetch();
										toast.success("Alert evaluation completed");
									} catch (error) {
										toast.error(getErrorMessage(error));
									}
								}}
								className="gap-2"
							>
								<BellRing className="h-4 w-4" /> {tr("evaluateNow")}
							</Button>
						)}
					</div>
				}
			/>
			<div className="mb-4 flex flex-wrap gap-2">
				<Select
					value={status}
					onValueChange={(value) => {
						setStatus(value as typeof status);
						setOffset(0);
					}}
				>
					<SelectTrigger className="w-44" data-testid="alert-history-status-filter">
						<SelectValue />
					</SelectTrigger>
					<SelectContent>
						<SelectItem value="all">{tr("allStatuses")}</SelectItem>
						<SelectItem value="sent">{tr("sent")}</SelectItem>
						<SelectItem value="failed">{tr("failed")}</SelectItem>
						<SelectItem value="skipped">{tr("skipped")}</SelectItem>
					</SelectContent>
				</Select>
				<Select
					value={scopeType}
					onValueChange={(value) => {
						setScopeType(value as typeof scopeType);
						setOffset(0);
					}}
				>
					<SelectTrigger className="w-44" data-testid="alert-history-scope-filter">
						<SelectValue />
					</SelectTrigger>
					<SelectContent>
						<SelectItem value="all">{tr("allScopes")}</SelectItem>
						{Object.entries(scopeLabels).map(([value, label]) => (
							<SelectItem key={value} value={value}>
								{label}
							</SelectItem>
						))}
					</SelectContent>
				</Select>
				<Select
					value={channelType}
					onValueChange={(value) => {
						setChannelType(value as typeof channelType);
						setOffset(0);
					}}
				>
					<SelectTrigger className="w-48" data-testid="alert-history-channel-filter">
						<SelectValue />
					</SelectTrigger>
					<SelectContent>
						<SelectItem value="all">{tr("allChannelTypes")}</SelectItem>
						{Object.entries(channelLabels).map(([value, label]) => (
							<SelectItem key={value} value={value}>
								{label}
							</SelectItem>
						))}
					</SelectContent>
				</Select>
			</div>
			{!isLoading && total === 0 ? (
				<EmptyState title={tr("noHistory")} body="Matched rule evaluations and delivery attempts will appear here." />
			) : (
				<div className="rounded-md border">
					<Table>
						<TableHeader>
							<TableRow>
								<TableHead>{tr("time")}</TableHead>
								<TableHead>{tr("rule")}</TableHead>
								<TableHead>{tr("channel")}</TableHead>
								<TableHead>{tr("scope")}</TableHead>
								<TableHead>{tr("status")}</TableHead>
								<TableHead>{tr("evaluation")}</TableHead>
								<TableHead>{tr("detail")}</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{rows.map((item) => (
								<TableRow key={item.id}>
									<TableCell className="text-xs whitespace-nowrap">{new Date(item.created_at).toLocaleString()}</TableCell>
									<TableCell>
										<div className="font-medium">{item.rule_name}</div>
										<code className="text-muted-foreground block max-w-48 truncate text-xs">{item.cel_expression}</code>
									</TableCell>
									<TableCell>
										{item.channel_name || "—"}
										<div className="text-muted-foreground text-xs">{item.channel_type ? channelLabels[item.channel_type] : ""}</div>
									</TableCell>
									<TableCell>
										{scopeLabels[item.scope_type]}
										<div className="text-muted-foreground max-w-36 truncate text-xs">{item.scope_id}</div>
									</TableCell>
									<TableCell>
										<Badge variant={statusVariant(item.status)}>{item.status}</Badge>
									</TableCell>
									<TableCell>
										<code className="text-muted-foreground block max-w-56 truncate text-xs">
											{Object.entries(item.input ?? {})
												.map(([key, value]) => `${key}: ${value}`)
												.join(", ")}
										</code>
									</TableCell>
									<TableCell className="text-muted-foreground max-w-52 text-xs">{item.status_detail || "—"}</TableCell>
								</TableRow>
							))}
						</TableBody>
					</Table>
					<div className="flex items-center justify-between border-t p-3 text-sm">
						<span className="text-muted-foreground">
							{total === 0 ? 0 : offset + 1}-{Math.min(offset + limit, total)} of {total}
						</span>
						<div className="flex gap-2">
							<Button variant="outline" size="sm" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - limit))}>
								{tr("previous")}
							</Button>
							<Button variant="outline" size="sm" disabled={offset + limit >= total} onClick={() => setOffset(offset + limit)}>
								{tr("next")}
							</Button>
						</div>
					</div>
				</div>
			)}
		</div>
	);
}