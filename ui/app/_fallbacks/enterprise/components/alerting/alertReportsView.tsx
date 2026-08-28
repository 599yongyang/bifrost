import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { ComboboxSelect } from "@/components/ui/combobox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Progress } from "@/components/ui/progress";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Badge } from "@/components/ui/badge";
import type {
	AlertChannel,
	DailyReportJobStatus,
	DailyReportPreview,
	DailyReportRunDetail,
	DailyReportSettings,
	DailyReportSettingsRequest,
} from "@/lib/types/alerting";
import {
	getErrorMessage,
	useDeliverDailyReportRunMutation,
	useGetAlertChannelsQuery,
	useGetDailyReportHistoryQuery,
	useGetDailyReportJobStatusQuery,
	useGetDailyReportRunQuery,
	useGetDailyReportSettingsQuery,
	useSendDailyReportNowMutation,
	useStartDailyReportJobMutation,
	useUpdateDailyReportSettingsMutation,
} from "@/lib/store";
import { getSupportedTimezones } from "@/lib/timezones";
import { cn } from "@/lib/utils";
import { ArrowRight, CalendarDays, CircleAlert, Database, Eye, RefreshCw, ScrollText, Send, ShieldCheck, Users } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import i18n from "@/lib/i18n";
import { summarizeDailyReportAudience } from "./dailyReportPresentation";

type SlowThresholdUnit = "ms" | "s" | "m";

const dailyReportJobStorageKey = "bifrost.daily-report-job-id";

function persistDailyReportJobID(id: string | null) {
	if (typeof window === "undefined") return;
	if (id) window.localStorage.setItem(dailyReportJobStorageKey, id);
	else window.localStorage.removeItem(dailyReportJobStorageKey);
}

type FormState = {
	enabled: boolean;
	timezone: string;
	generateTime: string;
	sendTime: string;
	slowValue: string;
	slowUnit: SlowThresholdUnit;
	internalEnabled: boolean;
	internalChannelIDs: string[];
	externalEnabled: boolean;
	externalChannelIDs: string[];
};

const t = (key: string, options?: Record<string, unknown>) => i18n.t(`workspace.alerting.${key}`, options);

function initialForm(settings?: DailyReportSettings): FormState {
	const threshold = fromMilliseconds(settings?.slow_threshold_ms ?? 10000);
	return {
		enabled: settings?.enabled ?? false,
		timezone: settings?.timezone ?? "Asia/Shanghai",
		generateTime: settings?.generate_time ?? "03:00",
		sendTime: settings?.send_time ?? "09:00",
		slowValue: String(threshold.value),
		slowUnit: threshold.unit,
		internalEnabled: settings?.internal_enabled ?? true,
		internalChannelIDs: settings?.internal_channel_ids ?? [],
		externalEnabled: settings?.external_enabled ?? false,
		externalChannelIDs: settings?.external_channel_ids ?? [],
	};
}

function fromMilliseconds(value: number): { value: number; unit: SlowThresholdUnit } {
	if (value > 0 && value % 60000 === 0) return { value: value / 60000, unit: "m" };
	if (value > 0 && value % 1000 === 0) return { value: value / 1000, unit: "s" };
	return { value, unit: "ms" };
}

function toMilliseconds(value: string, unit: SlowThresholdUnit): number {
	const numeric = Number(value || 0);
	if (!Number.isFinite(numeric) || numeric < 0) return 0;
	if (unit === "m") return Math.round(numeric * 60000);
	if (unit === "s") return Math.round(numeric * 1000);
	return Math.round(numeric);
}

function businessDateForTimezone(timezone: string): string {
	const formatter = new Intl.DateTimeFormat("en-CA", {
		timeZone: timezone || "UTC",
		year: "numeric",
		month: "2-digit",
		day: "2-digit",
	});
	const parts = formatter.formatToParts(new Date());
	const year = Number(parts.find((part) => part.type === "year")?.value ?? "1970");
	const month = Number(parts.find((part) => part.type === "month")?.value ?? "01");
	const day = Number(parts.find((part) => part.type === "day")?.value ?? "01");
	const localDay = new Date(Date.UTC(year, month - 1, day));
	localDay.setUTCDate(localDay.getUTCDate() - 1);
	return localDay.toISOString().slice(0, 10);
}

function formatLatency(ms: number): string {
	if (ms >= 1000) return `${(ms / 1000).toFixed(2)}s`;
	return `${ms.toFixed(0)}ms`;
}

function formatDateTime(value: string): string {
	return new Date(value).toLocaleString();
}

function channelOptions(channels: AlertChannel[]) {
	return channels
		.filter((channel) => ["slack", "microsoft_teams", "wecom", "webhook"].includes(channel.type))
		.map((channel) => ({
			value: channel.id,
			label: `${channel.name} · ${channel.type}`,
		}));
}

function audienceSummary(detail: DailyReportRunDetail, audience: "internal" | "external"): string {
	const summary = summarizeDailyReportAudience(detail, audience);
	if (summary.kind === "status") return t(`dailyReportsAudienceStatus.${summary.status}`);
	if (summary.failed === 0) return t("dailyReportsAudienceDelivered", summary);
	if (summary.delivered === 0) return t("dailyReportsAudienceFailed", summary);
	return t("dailyReportsAudienceMixed", summary);
}

function reportSettingsPayload(form: FormState): DailyReportSettingsRequest {
	return {
		enabled: form.enabled,
		timezone: form.timezone,
		generate_time: form.generateTime,
		send_time: form.sendTime,
		slow_threshold_ms: toMilliseconds(form.slowValue, form.slowUnit),
		internal_enabled: form.internalEnabled,
		internal_channel_ids: form.internalChannelIDs,
		external_enabled: form.externalEnabled,
		external_channel_ids: form.externalChannelIDs,
	};
}

function PreviewPanel({ preview, activeTab }: { preview: DailyReportPreview | undefined; activeTab: "internal" | "external" }) {
	if (!preview) {
		return (
			<div className="text-muted-foreground flex min-h-[360px] items-center justify-center rounded-sm border border-dashed text-sm">
				{t("dailyReportsPreviewEmpty")}
			</div>
		);
	}

	return (
		<div className="space-y-4">
			<div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
				<MetricCard icon={Users} label={t("dailyReportsRequests")} value={String(preview.snapshot.overview.user_requests)} />
				<MetricCard
					icon={ShieldCheck}
					label={t("dailyReportsUserSuccessRate")}
					value={`${preview.snapshot.overview.user_success_rate.toFixed(2)}%`}
				/>
				<MetricCard
					icon={RefreshCw}
					label={t("dailyReportsFallbackRecoveries")}
					value={String(preview.snapshot.overview.fallback_recoveries)}
				/>
				<MetricCard
					icon={CalendarDays}
					label={t("dailyReportsSlowRequests")}
					value={`${preview.snapshot.overview.slow_requests} · ${preview.snapshot.overview.slow_request_rate.toFixed(2)}%`}
				/>
			</div>
			<div className="bg-muted/30 rounded-sm border p-4">
				<div className="mb-2 flex items-center justify-between">
					<div>
						<p className="font-medium">{t("dailyReportsPreviewContent")}</p>
						<p className="text-muted-foreground text-xs">{t("dailyReportsPreviewContentDescription")}</p>
					</div>
					<Badge variant="outline">{preview.business_date}</Badge>
				</div>
				<pre className="bg-background max-h-[480px] overflow-auto rounded-sm p-4 text-xs leading-6 whitespace-pre-wrap">
					{activeTab === "internal" ? preview.internal_content : preview.external_content}
				</pre>
			</div>
		</div>
	);
}

function MetricCard({ icon: Icon, label, value }: { icon: typeof Users; label: string; value: string }) {
	return (
		<div className="bg-background rounded-sm border p-4">
			<div className="text-muted-foreground mb-2 flex items-center gap-2 text-xs">
				<Icon className="h-4 w-4" />
				<span>{label}</span>
			</div>
			<div className="text-base font-semibold">{value}</div>
		</div>
	);
}

function ScheduleFlow({ generateTime, sendTime }: { generateTime: string; sendTime: string }) {
	return (
		<div className="rounded-sm border border-emerald-600/20 bg-emerald-500/[0.04] p-4" data-testid="daily-reports-schedule-flow">
			<p className="text-foreground mb-3 text-sm font-medium">{t("dailyReportsFlowTitle")}</p>
			<div className="flex items-center gap-3">
				<div className="min-w-0 flex-1">
					<div className="mb-1 flex items-center gap-2">
						<Database className="h-4 w-4 shrink-0 text-emerald-700" />
						<span className="font-mono text-base font-semibold tabular-nums">{generateTime || "--:--"}</span>
					</div>
					<p className="text-muted-foreground text-xs">{t("dailyReportsFlowGenerate")}</p>
				</div>
				<ArrowRight className="text-muted-foreground h-4 w-4 shrink-0" aria-hidden="true" />
				<div className="min-w-0 flex-1">
					<div className="mb-1 flex items-center gap-2">
						<Send className="h-4 w-4 shrink-0 text-emerald-700" />
						<span className="font-mono text-base font-semibold tabular-nums">{sendTime || "--:--"}</span>
					</div>
					<p className="text-muted-foreground text-xs">{t("dailyReportsFlowSend")}</p>
				</div>
			</div>
			<p className="text-muted-foreground mt-3 border-t pt-3 text-xs">{t("dailyReportsTwoStageDescription")}</p>
		</div>
	);
}

function ReportJobProgress({ status }: { status: DailyReportJobStatus }) {
	const stage = status.stage || "pending";
	return (
		<Card className="border-emerald-600/25" data-testid="daily-reports-job-progress">
			<CardContent className="py-4">
				<div className="mb-3 flex items-start justify-between gap-4">
					<div>
						<p className="font-medium">{t("dailyReportsJobTitle")}</p>
						<p className="text-muted-foreground mt-1 text-sm">{t(`dailyReportsJobStage.${stage}`)}</p>
					</div>
					<Badge variant="outline">{Math.max(0, Math.min(100, status.percent ?? 0))}%</Badge>
				</div>
				<Progress value={status.percent ?? 0} className="h-2" />
				{status.processed ? (
					<p className="text-muted-foreground mt-2 font-mono text-xs tabular-nums">
						{t("dailyReportsJobProcessed", { count: status.processed.toLocaleString() })}
					</p>
				) : null}
				<p className="text-muted-foreground mt-2 text-xs">{t("dailyReportsJobPersistentHint")}</p>
			</CardContent>
		</Card>
	);
}

export default function AlertReportsView() {
	const canViewRules = useRbac(RbacResource.AlertRules, RbacOperation.View);
	const canViewChannels = useRbac(RbacResource.AlertChannels, RbacOperation.View);
	const canManage = useRbac(RbacResource.AlertRules, RbacOperation.Update) || useRbac(RbacResource.AlertRules, RbacOperation.Create);
	const hasAccess = canViewRules;

	const {
		data: settingsData,
		isLoading: settingsLoading,
		isError: settingsError,
		error: settingsLoadError,
		refetch: refetchSettings,
	} = useGetDailyReportSettingsQuery(undefined, { skip: !hasAccess });
	const { data: channelsData } = useGetAlertChannelsQuery(undefined, { skip: !hasAccess || !canViewChannels });
	const { data: runsData, isLoading: runsLoading, refetch: refetchRuns } = useGetDailyReportHistoryQuery(undefined, { skip: !hasAccess });
	const [updateSettings, updateSettingsState] = useUpdateDailyReportSettingsMutation();
	const [startReportJob, startJobState] = useStartDailyReportJobMutation();
	const [sendDailyReportNow, sendState] = useSendDailyReportNowMutation();
	const [deliverRun, deliverState] = useDeliverDailyReportRunMutation();

	const [form, setForm] = useState<FormState>(initialForm());
	const [businessDate, setBusinessDate] = useState(businessDateForTimezone("Asia/Shanghai"));
	const [previewTab, setPreviewTab] = useState<"internal" | "external">("internal");
	const [preview, setPreview] = useState<DailyReportPreview>();
	const [selectedRunID, setSelectedRunID] = useState<string | null>(null);
	const [previewRunID, setPreviewRunID] = useState<string | null>(null);
	const [jobID, setJobID] = useState<string | null>(() =>
		typeof window === "undefined" ? null : window.localStorage.getItem(dailyReportJobStorageKey),
	);
	const { data: jobStatus } = useGetDailyReportJobStatusQuery(
		{ id: jobID ?? undefined },
		{
			skip: !hasAccess || !jobID,
			pollingInterval: 1500,
		},
	);
	const { data: selectedRunData } = useGetDailyReportRunQuery(selectedRunID ?? "", { skip: !selectedRunID });
	const { data: previewRunData } = useGetDailyReportRunQuery(previewRunID ?? "", { skip: !previewRunID });

	useEffect(() => {
		if (settingsData?.settings) {
			setForm(initialForm(settingsData.settings));
			setBusinessDate(businessDateForTimezone(settingsData.settings.timezone));
		}
	}, [settingsData?.settings]);

	useEffect(() => {
		if (!jobStatus || !jobID) return;
		if (jobStatus.status === "completed") {
			if (jobStatus.run_id) {
				if (jobStatus.deliver) setSelectedRunID(jobStatus.run_id);
				else setPreviewRunID(jobStatus.run_id);
			}
			persistDailyReportJobID(null);
			setJobID(null);
			void refetchRuns();
		} else if (jobStatus.status === "failed") {
			toast.error(jobStatus.last_error || t("dailyReportsJobFailed"));
			persistDailyReportJobID(null);
			setJobID(null);
		}
	}, [jobID, jobStatus, refetchRuns]);

	useEffect(() => {
		if (!previewRunData || !settingsData?.settings) return;
		setPreview({
			business_date: previewRunData.run.business_date,
			settings: settingsData.settings,
			snapshot: previewRunData.run.snapshot,
			internal_content: previewRunData.run.internal_content,
			external_content: previewRunData.run.external_content,
		});
		setBusinessDate(previewRunData.run.business_date);
		setPreviewRunID(null);
	}, [previewRunData, settingsData?.settings]);

	if (!hasAccess) {
		return null;
	}

	const channels = channelsData?.channels ?? [];
	const supportedChannelOptions = channelOptions(channels);
	const selectedRun = selectedRunData ?? runsData?.runs.find((item) => item.run.id === selectedRunID);
	const selectedHasFrozenContent = Boolean(selectedRun?.run.internal_content || selectedRun?.run.external_content);
	const selectedFailureDetail = selectedRun?.run.internal_status_detail || selectedRun?.run.external_status_detail;
	const jobActive = jobStatus?.status === "pending" || jobStatus?.status === "running";

	async function handleSave() {
		try {
			await updateSettings(reportSettingsPayload(form)).unwrap();
			toast.success(t("dailyReportsSettingsSaved"));
			void refetchSettings();
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	}

	async function handlePreview() {
		try {
			const status = await startReportJob({ business_date: businessDate, deliver: false, settings: reportSettingsPayload(form) }).unwrap();
			if (status.id) {
				setJobID(status.id);
				persistDailyReportJobID(status.id);
			}
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	}

	async function handleGenerate() {
		try {
			const existing = runsData?.runs.find((item) => item.run.business_date === businessDate && item.run.timezone === form.timezone);
			const hasFrozenContent = Boolean(existing?.run.internal_content || existing?.run.external_content);
			if (existing && hasFrozenContent && existing.current_status !== "prepared") {
				setSelectedRunID(existing.run.id);
				toast.info(t("dailyReportsAlreadyGenerated"));
				return;
			}
			if (existing?.current_status === "prepared") {
				const result = await sendDailyReportNow({ business_date: businessDate }).unwrap();
				toast.success(t("dailyReportsGenerated"));
				setSelectedRunID(result.run.id);
				void refetchRuns();
				return;
			}
			const status = await startReportJob({ business_date: businessDate, deliver: true, settings: reportSettingsPayload(form) }).unwrap();
			if (status.id) {
				setJobID(status.id);
				persistDailyReportJobID(status.id);
			}
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	}

	async function handleRedeliver(audience: ("internal" | "external")[]) {
		if (!selectedRunID) return;
		try {
			await deliverRun({ id: selectedRunID, audience }).unwrap();
			toast.success(t("dailyReportsRedeliveryTriggered"));
			void refetchRuns();
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	}

	async function handleSelectedRunAction() {
		if (!selectedRun) return;
		try {
			if (selectedRun.current_status === "prepared") {
				const result = await sendDailyReportNow({ business_date: selectedRun.run.business_date }).unwrap();
				toast.success(t("dailyReportsPreparedSent"));
				setSelectedRunID(result.run.id);
				void refetchRuns();
				return;
			}
			const status = await startReportJob({
				business_date: selectedRun.run.business_date,
				deliver: true,
				settings: reportSettingsPayload(form),
			}).unwrap();
			if (status.id) {
				setJobID(status.id);
				persistDailyReportJobID(status.id);
				setSelectedRunID(null);
			}
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	}

	return (
		<div className="space-y-5 p-4 md:p-6">
			<div className="flex flex-col gap-3 md:flex-row md:items-end md:justify-between">
				<div>
					<div className="flex items-center gap-2">
						<h1 className="text-lg font-semibold">{t("dailyReportsTitle")}</h1>
						<Badge variant="outline" className="border-emerald-500/40 text-emerald-700">
							Beta
						</Badge>
					</div>
					<p className="text-muted-foreground text-sm">{t("dailyReportsDescription")}</p>
				</div>
				<div className="flex flex-wrap items-center gap-2">
					<Input
						type="date"
						value={businessDate}
						onChange={(event) => setBusinessDate(event.target.value)}
						className="w-[180px]"
						data-testid="daily-reports-business-date"
					/>
					<Button
						variant="outline"
						onClick={() => void handlePreview()}
						disabled={startJobState.isLoading || jobActive}
						data-testid="daily-reports-preview"
					>
						<Eye className="mr-2 h-4 w-4" />
						{t("dailyReportsPreview")}
					</Button>
					<Button
						onClick={() => void handleGenerate()}
						disabled={!canManage || sendState.isLoading || startJobState.isLoading || jobActive}
						data-testid="daily-reports-generate"
					>
						<Send className="mr-2 h-4 w-4" />
						{t("dailyReportsGenerate")}
					</Button>
				</div>
			</div>
			{jobActive && jobStatus ? <ReportJobProgress status={jobStatus} /> : null}

			<div className="grid gap-5 xl:grid-cols-[minmax(0,460px)_minmax(0,1fr)]">
				<Card>
					<CardHeader>
						<CardTitle>{t("dailyReportsConfigTitle")}</CardTitle>
						<CardDescription>{t("dailyReportsConfigDescription")}</CardDescription>
					</CardHeader>
					<CardContent className="space-y-5">
						{settingsError ? (
							<div className="border-destructive/30 rounded-sm border p-4 text-sm">
								<div className="font-medium">{t("loadError")}</div>
								<div className="text-muted-foreground mt-1">{getErrorMessage(settingsLoadError)}</div>
							</div>
						) : null}
						<div className="rounded-sm border p-4">
							<div className="flex items-start justify-between gap-4">
								<div>
									<p className="font-medium">{t("dailyReportsMasterSwitch")}</p>
									<p className="text-muted-foreground text-sm">{t("dailyReportsMasterSwitchDescription")}</p>
								</div>
								<Switch
									checked={form.enabled}
									onCheckedChange={(checked) => setForm((current) => ({ ...current, enabled: checked }))}
									disabled={!canManage}
									data-testid="daily-reports-enabled"
								/>
							</div>
						</div>
						<ScheduleFlow generateTime={form.generateTime} sendTime={form.sendTime} />

						<div className="space-y-2">
							<Label>{t("dailyReportsTimezone")}</Label>
							<ComboboxSelect
								options={getSupportedTimezones().map((timezone) => ({ value: timezone, label: timezone }))}
								value={form.timezone}
								onValueChange={(timezone) =>
									setForm((current) => ({
										...current,
										timezone: timezone || current.timezone,
									}))
								}
								placeholder={t("dailyReportsTimezonePlaceholder")}
								data-testid="daily-reports-timezone"
							/>
						</div>
						<div className="grid gap-4 sm:grid-cols-2">
							<div className="space-y-2">
								<Label>{t("dailyReportsGenerateTime")}</Label>
								<Input
									type="time"
									value={form.generateTime}
									onChange={(event) => setForm((current) => ({ ...current, generateTime: event.target.value }))}
									disabled={!canManage}
									data-testid="daily-reports-generate-time"
								/>
							</div>
							<div className="space-y-2">
								<Label>{t("dailyReportsSendTime")}</Label>
								<Input
									type="time"
									value={form.sendTime}
									onChange={(event) => setForm((current) => ({ ...current, sendTime: event.target.value }))}
									disabled={!canManage}
									data-testid="daily-reports-send-time"
								/>
							</div>
						</div>
						<div className="space-y-2">
							<Label>{t("dailyReportsSlowThreshold")}</Label>
							<div className="grid gap-3 md:grid-cols-[minmax(0,1fr)_120px]">
								<Input
									type="number"
									min="0"
									value={form.slowValue}
									onChange={(event) => setForm((current) => ({ ...current, slowValue: event.target.value }))}
									disabled={!canManage}
									data-testid="daily-reports-slow-threshold-value"
								/>
								<Select
									value={form.slowUnit}
									onValueChange={(value: SlowThresholdUnit) => setForm((current) => ({ ...current, slowUnit: value }))}
								>
									<SelectTrigger data-testid="daily-reports-slow-threshold-unit">
										<SelectValue />
									</SelectTrigger>
									<SelectContent>
										<SelectItem value="ms">{t("dailyReportsMilliseconds")}</SelectItem>
										<SelectItem value="s">{t("dailyReportsSeconds")}</SelectItem>
										<SelectItem value="m">{t("dailyReportsMinutes")}</SelectItem>
									</SelectContent>
								</Select>
							</div>
							<p className="text-muted-foreground text-xs">{t("dailyReportsSlowThresholdDescription")}</p>
						</div>

						<AudienceCard
							icon={ScrollText}
							title={t("dailyReportsInternalAudience")}
							description={t("dailyReportsInternalAudienceDescription")}
							enabled={form.internalEnabled}
							channelIDs={form.internalChannelIDs}
							options={supportedChannelOptions}
							disabled={!canManage}
							onEnabledChange={(enabled) => setForm((current) => ({ ...current, internalEnabled: enabled }))}
							onChannelsChange={(internalChannelIDs) => setForm((current) => ({ ...current, internalChannelIDs }))}
							testIdPrefix="daily-reports-internal"
						/>
						<AudienceCard
							icon={Users}
							title={t("dailyReportsExternalAudience")}
							description={t("dailyReportsExternalAudienceDescription")}
							enabled={form.externalEnabled}
							channelIDs={form.externalChannelIDs}
							options={supportedChannelOptions}
							disabled={!canManage}
							onEnabledChange={(enabled) => setForm((current) => ({ ...current, externalEnabled: enabled }))}
							onChannelsChange={(externalChannelIDs) => setForm((current) => ({ ...current, externalChannelIDs }))}
							testIdPrefix="daily-reports-external"
						/>
					</CardContent>
					<CardFooter className="justify-between border-t">
						<Button
							variant="outline"
							onClick={() => settingsData?.settings && setForm(initialForm(settingsData.settings))}
							disabled={settingsLoading}
							data-testid="daily-reports-reset"
						>
							{t("dailyReportsReset")}
						</Button>
						<Button
							onClick={() => void handleSave()}
							disabled={!canManage || updateSettingsState.isLoading}
							data-testid="daily-reports-save"
						>
							{t("saveChanges")}
						</Button>
					</CardFooter>
				</Card>

				<Card>
					<CardHeader>
						<CardTitle>{t("dailyReportsPreviewTitle")}</CardTitle>
						<CardDescription>{t("dailyReportsPreviewDescription")}</CardDescription>
					</CardHeader>
					<CardContent>
						<Tabs value={previewTab} onValueChange={(value) => setPreviewTab(value as "internal" | "external")}>
							<TabsList>
								<TabsTrigger value="internal" data-testid="daily-reports-preview-tab-internal">
									{t("dailyReportsInternalAudience")}
								</TabsTrigger>
								<TabsTrigger value="external" data-testid="daily-reports-preview-tab-external">
									{t("dailyReportsExternalAudience")}
								</TabsTrigger>
							</TabsList>
							<TabsContent value="internal" className="pt-3">
								<PreviewPanel preview={preview} activeTab="internal" />
							</TabsContent>
							<TabsContent value="external" className="pt-3">
								<PreviewPanel preview={preview} activeTab="external" />
							</TabsContent>
						</Tabs>
					</CardContent>
				</Card>
			</div>

			<Card>
				<CardHeader>
					<CardTitle>{t("dailyReportsHistoryTitle")}</CardTitle>
					<CardDescription>{t("dailyReportsHistoryDescription")}</CardDescription>
				</CardHeader>
				<CardContent>
					<div className="overflow-x-auto">
						<Table data-testid="daily-reports-history-table">
							<TableHeader>
								<TableRow>
									<TableHead>{t("dailyReportsHistoryDate")}</TableHead>
									<TableHead>{t("dailyReportsHistoryGeneratedAt")}</TableHead>
									<TableHead>{t("dailyReportsHistoryTrigger")}</TableHead>
									<TableHead>{t("dailyReportsOverallStatus")}</TableHead>
									<TableHead>{t("dailyReportsInternalAudience")}</TableHead>
									<TableHead>{t("dailyReportsExternalAudience")}</TableHead>
									<TableHead>{t("detail")}</TableHead>
								</TableRow>
							</TableHeader>
							<TableBody>
								{runsLoading ? (
									<TableRow>
										<TableCell colSpan={7} className="text-muted-foreground text-center">
											{t("dailyReportsLoading")}
										</TableCell>
									</TableRow>
								) : runsData?.runs.length ? (
									runsData.runs.map((detail) => (
										<TableRow key={detail.run.id}>
											<TableCell>{detail.run.business_date}</TableCell>
											<TableCell>{formatDateTime(detail.run.generated_at)}</TableCell>
											<TableCell className="capitalize">{detail.run.trigger}</TableCell>
											<TableCell>
												<Badge variant="outline">{t(`dailyReportsStatus.${detail.current_status}`)}</Badge>
											</TableCell>
											<TableCell>{audienceSummary(detail, "internal")}</TableCell>
											<TableCell>{audienceSummary(detail, "external")}</TableCell>
											<TableCell>
												<Button
													variant="outline"
													size="sm"
													onClick={() => setSelectedRunID(detail.run.id)}
													data-testid={`daily-reports-view-${detail.run.id}`}
												>
													<Eye className="mr-2 h-4 w-4" />
													{t("dailyReportsView")}
												</Button>
											</TableCell>
										</TableRow>
									))
								) : (
									<TableRow>
										<TableCell colSpan={7} className="text-muted-foreground text-center">
											{t("dailyReportsHistoryEmpty")}
										</TableCell>
									</TableRow>
								)}
							</TableBody>
						</Table>
					</div>
				</CardContent>
			</Card>

			<Dialog open={!!selectedRunID} onOpenChange={(open) => !open && setSelectedRunID(null)}>
				<DialogContent className="max-h-[88vh] overflow-hidden sm:max-w-5xl">
					<DialogHeader>
						<DialogTitle>{t("dailyReportsDetailTitle")}</DialogTitle>
						<DialogDescription>
							{selectedRun ? `${selectedRun.run.business_date} · ${selectedRun.run.timezone}` : t("dailyReportsLoading")}
						</DialogDescription>
					</DialogHeader>
					{selectedRun ? (
						<div className="space-y-4 overflow-y-auto pr-1">
							{selectedHasFrozenContent ? (
								<div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
									<MetricCard
										icon={Users}
										label={t("dailyReportsRequests")}
										value={String(selectedRun.run.snapshot.overview.user_requests)}
									/>
									<MetricCard
										icon={ShieldCheck}
										label={t("dailyReportsUserSuccessRate")}
										value={`${selectedRun.run.snapshot.overview.user_success_rate.toFixed(2)}%`}
									/>
									<MetricCard
										icon={RefreshCw}
										label={t("dailyReportsFallbackRecoveries")}
										value={String(selectedRun.run.snapshot.overview.fallback_recoveries)}
									/>
									<MetricCard
										icon={CalendarDays}
										label={t("dailyReportsGeneratedAt")}
										value={formatDateTime(selectedRun.run.generated_at)}
									/>
								</div>
							) : (
								<div
									className="border-destructive/30 bg-destructive/[0.04] rounded-sm border p-4"
									data-testid="daily-reports-generation-error"
								>
									<div className="flex items-start gap-3">
										<CircleAlert className="text-destructive mt-0.5 h-5 w-5 shrink-0" />
										<div>
											<p className="font-medium">{t("dailyReportsGenerationFailedTitle")}</p>
											<p className="text-muted-foreground mt-1 text-sm">
												{selectedFailureDetail || t("dailyReportsGenerationFailedDescription")}
											</p>
										</div>
									</div>
								</div>
							)}

							{selectedRun.deliveries.length > 0 ? (
								<div className="rounded-sm border">
									<div className="border-b px-4 py-3 font-medium">{t("dailyReportsDeliveryLog")}</div>
									<div className="overflow-x-auto">
										<Table>
											<TableHeader>
												<TableRow>
													<TableHead>{t("channel")}</TableHead>
													<TableHead>{t("dailyReportsAudience")}</TableHead>
													<TableHead>{t("status")}</TableHead>
													<TableHead>{t("time")}</TableHead>
													<TableHead>{t("detail")}</TableHead>
												</TableRow>
											</TableHeader>
											<TableBody>
												{selectedRun.deliveries.map((delivery) => (
													<TableRow key={delivery.id}>
														<TableCell>{delivery.channel_name || delivery.channel_id}</TableCell>
														<TableCell>{delivery.audience}</TableCell>
														<TableCell>
															<span
																className={cn(
																	"inline-flex rounded-full px-2 py-0.5 text-xs font-medium",
																	delivery.status === "delivered" ? "bg-emerald-100 text-emerald-700" : "bg-rose-100 text-rose-700",
																)}
															>
																{delivery.status}
															</span>
														</TableCell>
														<TableCell>{formatDateTime(delivery.created_at)}</TableCell>
														<TableCell className="max-w-[320px] truncate" title={delivery.status_detail || undefined}>
															{delivery.status_detail || "-"}
														</TableCell>
													</TableRow>
												))}
											</TableBody>
										</Table>
									</div>
								</div>
							) : null}

							{selectedHasFrozenContent ? (
								<Tabs defaultValue="internal">
									<TabsList>
										<TabsTrigger value="internal" data-testid="daily-reports-detail-tab-internal">
											{t("dailyReportsInternalAudience")}
										</TabsTrigger>
										<TabsTrigger value="external" data-testid="daily-reports-detail-tab-external">
											{t("dailyReportsExternalAudience")}
										</TabsTrigger>
									</TabsList>
									<TabsContent value="internal" className="pt-3">
										<pre className="bg-muted/30 max-h-[320px] overflow-auto rounded-sm border p-4 text-xs leading-6 whitespace-pre-wrap">
											{selectedRun.run.internal_content}
										</pre>
									</TabsContent>
									<TabsContent value="external" className="pt-3">
										<pre className="bg-muted/30 max-h-[320px] overflow-auto rounded-sm border p-4 text-xs leading-6 whitespace-pre-wrap">
											{selectedRun.run.external_content}
										</pre>
									</TabsContent>
								</Tabs>
							) : null}
						</div>
					) : (
						<div className="text-muted-foreground py-10 text-center text-sm">{t("dailyReportsLoading")}</div>
					)}
					<DialogFooter className="gap-2 sm:justify-between">
						{canManage && selectedRun ? (
							<div className="flex flex-wrap gap-2">
								{!selectedHasFrozenContent ? (
									<Button
										onClick={() => void handleSelectedRunAction()}
										disabled={sendState.isLoading || selectedRun.current_status === "running"}
										data-testid="daily-reports-regenerate"
									>
										<RefreshCw className="mr-2 h-4 w-4" />
										{selectedRun.current_status === "running" ? t("dailyReportsGenerating") : t("dailyReportsRegenerate")}
									</Button>
								) : selectedRun.current_status === "prepared" ? (
									<Button
										onClick={() => void handleSelectedRunAction()}
										disabled={sendState.isLoading}
										data-testid="daily-reports-send-prepared"
									>
										<Send className="mr-2 h-4 w-4" />
										{t("dailyReportsSendPrepared")}
									</Button>
								) : (
									<>
										{selectedRun.run.internal_content && selectedRun.run.internal_channel_ids.length > 0 ? (
											<Button
												variant="outline"
												onClick={() => void handleRedeliver(["internal"])}
												disabled={deliverState.isLoading}
												data-testid="daily-reports-resend-internal"
											>
												{t("dailyReportsResendInternal")}
											</Button>
										) : null}
										{selectedRun.run.external_content && selectedRun.run.external_channel_ids.length > 0 ? (
											<Button
												variant="outline"
												onClick={() => void handleRedeliver(["external"])}
												disabled={deliverState.isLoading}
												data-testid="daily-reports-resend-external"
											>
												{t("dailyReportsResendExternal")}
											</Button>
										) : null}
										{selectedRun.run.internal_channel_ids.length > 0 && selectedRun.run.external_channel_ids.length > 0 ? (
											<Button
												onClick={() => void handleRedeliver(["internal", "external"])}
												disabled={deliverState.isLoading}
												data-testid="daily-reports-resend-both"
											>
												{t("dailyReportsResendBoth")}
											</Button>
										) : null}
									</>
								)}
							</div>
						) : null}
					</DialogFooter>
				</DialogContent>
			</Dialog>
		</div>
	);
}

function AudienceCard({
	icon: Icon,
	title,
	description,
	enabled,
	channelIDs,
	options,
	disabled,
	onEnabledChange,
	onChannelsChange,
	testIdPrefix,
}: {
	icon: typeof ScrollText;
	title: string;
	description: string;
	enabled: boolean;
	channelIDs: string[];
	options: { value: string; label: string }[];
	disabled: boolean;
	onEnabledChange: (enabled: boolean) => void;
	onChannelsChange: (values: string[]) => void;
	testIdPrefix: string;
}) {
	return (
		<div className="rounded-sm border p-4">
			<div className="mb-4 flex items-start justify-between gap-4">
				<div className="flex items-start gap-3">
					<div className="bg-muted rounded-sm p-2">
						<Icon className="h-4 w-4" />
					</div>
					<div>
						<p className="font-medium">{title}</p>
						<p className="text-muted-foreground text-sm">{description}</p>
					</div>
				</div>
				<Switch checked={enabled} onCheckedChange={onEnabledChange} disabled={disabled} data-testid={`${testIdPrefix}-enabled`} />
			</div>
			<div className="space-y-2">
				<Label>{t("channels")}</Label>
				<ComboboxSelect
					multiple
					options={options}
					value={channelIDs}
					onValueChange={onChannelsChange}
					placeholder={t("dailyReportsChannelsPlaceholder")}
					disabled={disabled}
					data-testid={`${testIdPrefix}-channels`}
				/>
				<p className="text-muted-foreground text-xs">{t("dailyReportsChannelHint")}</p>
			</div>
		</div>
	);
}