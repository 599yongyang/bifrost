import i18n from "@/lib/i18n";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { ComboboxSelect } from "@/components/ui/combobox";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Progress } from "@/components/ui/progress";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
	getErrorMessage,
	useDeliverDailyReportRunMutation,
	useGetAlertChannelsQuery,
	useGetDailyReportHistoryQuery,
	useGetDailyReportJobStatusQuery,
	useGetDailyReportRunQuery,
	useGetDailyReportSettingsQuery,
	usePreviewDailyReportMutation,
	useStartDailyReportJobMutation,
	useUpdateDailyReportSettingsMutation,
} from "@/lib/store";
import type { DailyReportAudience, DailyReportJobStatus, DailyReportPreview, DailyReportRunDetail } from "@/lib/types/alerting";
import { getSupportedTimezones } from "@/lib/timezones";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import {
	ArrowRight,
	CalendarClock,
	ChevronLeft,
	ChevronRight,
	CircleAlert,
	CircleGauge,
	Clock3,
	Eye,
	FileCheck2,
	Play,
	RotateCcw,
	Save,
	Send,
	UsersRound,
} from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { alertingCopy } from "./copy";
import {
	dailyReportPermissions,
	externalPreviewProjection,
	formatDailyReportPercent,
	isDailyReportPreview,
	serializeDailyReportPreviewSettings,
	serializeDailyReportSettings,
	settingsToForm,
	shouldPollDailyReportJob,
	type DailyReportSettingsForm,
} from "./dailyReportModel";

const copy = alertingCopy();
const DAILY_REPORT_JOB_STORAGE_KEY = "bifrost.daily-report.active-job.v1";
const defaultForm: DailyReportSettingsForm = {
	enabled: false,
	timezone: "Asia/Shanghai",
	generate_time: "03:00",
	send_time: "09:00",
	slow_threshold_ms: 10000,
	internal_enabled: true,
	internal_channel_ids: [],
	external_enabled: false,
	external_channel_ids: [],
};

function ReportTimeSelect({
	value,
	onChange,
	disabled,
	testId,
	label,
}: {
	value: string;
	onChange: (value: string) => void;
	disabled: boolean;
	testId: string;
	label: string;
}) {
	const [hour = "00", minute = "00"] = value.split(":");
	return (
		<div className="grid grid-cols-2 gap-2">
			<Select value={hour} onValueChange={(nextHour) => onChange(`${nextHour}:${minute}`)} disabled={disabled}>
				<SelectTrigger className="w-full" data-testid={`${testId}-hour`} aria-label={`${label} hour`}>
					<SelectValue />
				</SelectTrigger>
				<SelectContent>
					{Array.from({ length: 24 }, (_, index) => String(index).padStart(2, "0")).map((item) => (
						<SelectItem key={item} value={item}>
							{item}
						</SelectItem>
					))}
				</SelectContent>
			</Select>
			<Select value={minute} onValueChange={(nextMinute) => onChange(`${hour}:${nextMinute}`)} disabled={disabled}>
				<SelectTrigger className="w-full" data-testid={`${testId}-minute`} aria-label={`${label} minute`}>
					<SelectValue />
				</SelectTrigger>
				<SelectContent>
					{Array.from({ length: 60 }, (_, index) => String(index).padStart(2, "0")).map((item) => (
						<SelectItem key={item} value={item}>
							{item}
						</SelectItem>
					))}
				</SelectContent>
			</Select>
		</div>
	);
}

function restoreDailyReportJob(): DailyReportJobStatus | null {
	if (typeof window === "undefined") return null;
	try {
		const parsed = JSON.parse(window.localStorage.getItem(DAILY_REPORT_JOB_STORAGE_KEY) ?? "null") as DailyReportJobStatus | null;
		return parsed?.id && parsed.status ? parsed : null;
	} catch {
		window.localStorage.removeItem(DAILY_REPORT_JOB_STORAGE_KEY);
		return null;
	}
}

function AudienceChannels({
	title,
	description,
	testId,
	enabled,
	ids,
	channels,
	disabled,
	onEnabled,
	onIDs,
}: {
	title: string;
	description: string;
	testId: string;
	enabled: boolean;
	ids: string[];
	channels: { id: string; name: string; enabled: boolean; type: string }[];
	disabled: boolean;
	onEnabled: (value: boolean) => void;
	onIDs: (ids: string[]) => void;
}) {
	return (
		<div className="overflow-hidden rounded-md border">
			<div className="bg-muted/15 flex items-start justify-between gap-4 border-b px-4 py-3.5">
				<div>
					<Label className="text-sm font-semibold">{title}</Label>
					<p className="text-muted-foreground mt-1 text-xs leading-relaxed">{description}</p>
				</div>
				<Switch data-testid={`${testId}-enabled`} checked={enabled} onCheckedChange={onEnabled} disabled={disabled} />
			</div>
			<div className="p-3">
				{enabled ? (
					<div className="grid gap-2 sm:grid-cols-2">
						{channels
							.filter((channel) => channel.enabled && channel.type !== "pagerduty")
							.map((channel) => (
								<label
									key={channel.id}
									className="hover:bg-muted/40 has-data-[state=checked]:border-primary/40 has-data-[state=checked]:bg-primary/5 flex cursor-pointer items-center gap-3 rounded-md border px-3 py-2.5 transition-colors duration-150"
								>
									<Checkbox
										data-testid={`${testId}-channel-${channel.id}`}
										disabled={disabled}
										checked={ids.includes(channel.id)}
										onCheckedChange={(checked) => onIDs(checked ? [...ids, channel.id] : ids.filter((id) => id !== channel.id))}
									/>
									<span className="min-w-0">
										<span className="block truncate text-sm font-medium">{channel.name}</span>
										<span className="text-muted-foreground block text-xs">{channel.type}</span>
									</span>
								</label>
							))}
						{channels.filter((channel) => channel.enabled && channel.type !== "pagerduty").length === 0 ? (
							<p className="text-muted-foreground col-span-full px-1 py-3 text-sm">{i18n.t("workspace.alerting.dailyReportsNoChannels")}</p>
						) : null}
					</div>
				) : (
					<p className="text-muted-foreground px-1 py-2 text-sm">{copy.disabled}</p>
				)}
			</div>
		</div>
	);
}

function PreviewPanels({
	preview,
}: {
	preview: Pick<DailyReportPreview, "business_date" | "snapshot" | "internal_content" | "external_content">;
}) {
	const external = externalPreviewProjection(preview);
	return (
		<Tabs defaultValue="internal">
			<TabsList>
				<TabsTrigger value="internal">{copy.internal}</TabsTrigger>
				<TabsTrigger value="external">{copy.external}</TabsTrigger>
			</TabsList>
			<TabsContent value="internal" className="space-y-3">
				<div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
					{[
						[i18n.t("workspace.alerting.dailyReportsRequests"), preview.snapshot.overview.user_requests.toLocaleString()],
						[
							i18n.t("workspace.alerting.dailyReportsUserSuccessRate"),
							formatDailyReportPercent(preview.snapshot.overview.user_success_rate),
						],
						[i18n.t("workspace.alerting.dailyReportsFallbackRecoveries"), preview.snapshot.overview.fallback_recoveries.toLocaleString()],
						[i18n.t("workspace.alerting.dailyReportsSlowRequests"), preview.snapshot.overview.slow_requests.toLocaleString()],
					].map(([label, value]) => (
						<div key={label} className="bg-muted/30 rounded-sm border p-3">
							<p className="text-muted-foreground text-xs">{label}</p>
							<p className="mt-1 text-lg font-semibold tabular-nums">{value}</p>
						</div>
					))}
				</div>
				<pre className="bg-muted max-h-80 overflow-auto rounded p-4 text-xs whitespace-pre-wrap">{preview.internal_content}</pre>
				<div className="grid gap-2 sm:grid-cols-3">
					{preview.snapshot.providers.map((provider) => (
						<div key={provider.provider} className="rounded border p-3">
							<div className="font-medium">{provider.provider}</div>
							<div className="text-muted-foreground text-xs">
								{provider.attempts} / {formatDailyReportPercent(provider.success_rate)}
							</div>
						</div>
					))}
				</div>
			</TabsContent>
			<TabsContent value="external" className="space-y-3">
				<p className="text-muted-foreground text-sm">{copy.externalPrivacy}</p>
				<pre className="bg-muted max-h-80 overflow-auto rounded p-4 text-xs whitespace-pre-wrap">{external.external_content}</pre>
				<pre className="bg-muted max-h-64 overflow-auto rounded p-4 text-xs">{JSON.stringify(external.snapshot, null, 2)}</pre>
			</TabsContent>
		</Tabs>
	);
}

export function DailyReportsView() {
	const canView = useRbac(RbacResource.AlertHistory, RbacOperation.View);
	const canUpdate = useRbac(RbacResource.AlertHistory, RbacOperation.Update);
	const permissions = dailyReportPermissions(canView, canUpdate);
	const { data: settingsData } = useGetDailyReportSettingsQuery(undefined, { skip: !permissions.canView });
	const { data: channelsData } = useGetAlertChannelsQuery(undefined, { skip: !permissions.canView });
	const [form, setForm] = useState(defaultForm);
	const [businessDate, setBusinessDate] = useState("");
	const [preview, setPreview] = useState<DailyReportPreview | null>(null);
	const [previewPending, setPreviewPending] = useState(false);
	const [job, setJob] = useState<DailyReportJobStatus | null>(restoreDailyReportJob);
	const [historyRefreshKey, setHistoryRefreshKey] = useState(0);
	const previousJobStatus = useRef(job?.status);
	const [saveSettings, saving] = useUpdateDailyReportSettingsMutation();
	const [requestPreview, previewing] = usePreviewDailyReportMutation();
	const [startJob, startingJob] = useStartDailyReportJobMutation();
	const { data: jobStatus } = useGetDailyReportJobStatusQuery(job?.id ? { id: job.id } : undefined, {
		skip: !job?.id,
		pollingInterval: shouldPollDailyReportJob(job ?? undefined) ? 1500 : 0,
	});
	const jobActive = shouldPollDailyReportJob(job ?? undefined);
	const timezoneOptions = useMemo(() => getSupportedTimezones().map((timezone) => ({ value: timezone, label: timezone })), []);
	useEffect(() => {
		if (settingsData?.settings) setForm(settingsToForm(settingsData.settings));
	}, [settingsData]);
	useEffect(() => {
		if (jobStatus) setJob(jobStatus);
	}, [jobStatus]);
	useEffect(() => {
		if (typeof window === "undefined") return;
		if (job?.id) window.localStorage.setItem(DAILY_REPORT_JOB_STORAGE_KEY, JSON.stringify(job));
		else window.localStorage.removeItem(DAILY_REPORT_JOB_STORAGE_KEY);
	}, [job]);
	useEffect(() => {
		if (job?.status === "completed" && previousJobStatus.current !== "completed") setHistoryRefreshKey((value) => value + 1);
		previousJobStatus.current = job?.status;
	}, [job?.status]);
	useEffect(() => {
		if (!previewPending || jobStatus?.status !== "completed") return;
		setPreviewPending(false);
		requestPreview({ business_date: businessDate || undefined, settings: serializeDailyReportPreviewSettings(form) })
			.unwrap()
			.then((result) => {
				if (isDailyReportPreview(result)) setPreview(result.preview);
			})
			.catch((error) => toast.error(getErrorMessage(error)));
	}, [businessDate, form, jobStatus?.status, previewPending, requestPreview]);
	const channels = channelsData?.channels ?? [];
	const save = async () => {
		try {
			const updated = await saveSettings(serializeDailyReportSettings(form)).unwrap();
			setForm(settingsToForm(updated));
			toast.success(copy.reportSaved);
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	};
	const doPreview = async () => {
		if (!permissions.canPreview) return;
		try {
			const result = await requestPreview({
				business_date: businessDate || undefined,
				settings: serializeDailyReportPreviewSettings(form),
			}).unwrap();
			if (isDailyReportPreview(result)) {
				setPreview(result.preview);
				setJob(null);
			} else {
				setJob(result);
				setPreviewPending(true);
				toast.success(copy.jobStarted);
			}
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	};
	const run = async (deliver: boolean) => {
		if (!permissions.canGenerate) return;
		try {
			const result = await startJob({
				business_date: businessDate || undefined,
				deliver,
				settings: serializeDailyReportSettings(form),
			}).unwrap();
			setJob(result);
			setPreviewPending(false);
			toast.success(copy.jobStarted);
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	};
	return (
		<div className="max-w-7xl space-y-5" data-testid="daily-reports-view">
			<div className="flex flex-wrap items-start justify-between gap-3">
				<div>
					<div className="flex items-center gap-2">
						<h1 className="text-lg font-semibold">{copy.dailyReports}</h1>
						<Badge variant="outline" className="border-emerald-500/30 text-emerald-700 dark:text-emerald-400">
							{copy.beta}
						</Badge>
					</div>
					<p className="text-muted-foreground mt-1 max-w-2xl text-sm">{copy.dailyReportsDescription}</p>
				</div>
				<Badge variant={form.enabled ? "default" : "secondary"}>
					{form.enabled ? i18n.t("workspace.alerting.dailyReportsRunning") : i18n.t("workspace.alerting.dailyReportsPaused")}
				</Badge>
			</div>
			<Tabs defaultValue="settings" className="gap-4">
				<TabsList className="w-fit">
					<TabsTrigger value="settings">{copy.settings}</TabsTrigger>
					<TabsTrigger value="preview">{copy.preview}</TabsTrigger>
					<TabsTrigger value="history">{copy.reportHistory}</TabsTrigger>
				</TabsList>
				<TabsContent value="settings" className="space-y-4">
					<div className="overflow-hidden rounded-md border">
						<div className="bg-muted/15 grid gap-3 px-4 py-3.5 md:grid-cols-[1fr_auto_1fr_auto_1fr] md:items-center">
							{[
								{ icon: Clock3, title: copy.generateTime, value: `${form.generate_time} · ${form.timezone}` },
								{
									icon: FileCheck2,
									title: i18n.t("workspace.alerting.dailyReportsFlowGenerate"),
									value: `${copy.slowThreshold}: ${form.slow_threshold_ms} ms`,
								},
								{ icon: Send, title: copy.sendTime, value: `${form.send_time} · ${copy.internal}/${copy.external}` },
							].map((step, index) => (
								<div key={step.title} className="contents">
									{index > 0 ? <ArrowRight className="text-muted-foreground hidden size-4 md:block" /> : null}
									<div className="flex min-w-0 items-center gap-3">
										<div className="bg-background flex size-8 shrink-0 items-center justify-center rounded-full border">
											<step.icon className="text-muted-foreground size-4" />
										</div>
										<div className="min-w-0">
											<p className="truncate text-sm font-medium">{step.title}</p>
											<p className="text-muted-foreground truncate text-xs">{step.value}</p>
										</div>
									</div>
								</div>
							))}
						</div>

						<div className="grid xl:grid-cols-[minmax(0,1.45fr)_minmax(20rem,0.75fr)]">
							<section className="border-b p-5 xl:border-r xl:border-b-0">
								<div className="mb-4 flex items-start gap-3">
									<CalendarClock className="text-muted-foreground mt-0.5 size-4" />
									<div>
										<h2 className="text-sm font-semibold">{i18n.t("workspace.alerting.dailyReportsScheduleTitle")}</h2>
										<p className="text-muted-foreground mt-1 text-xs">{i18n.t("workspace.alerting.dailyReportsScheduleDescription")}</p>
									</div>
								</div>
								<div className="grid gap-4 sm:grid-cols-2">
									<div className="space-y-1.5 sm:col-span-2">
										<Label>{copy.timezone}</Label>
										<ComboboxSelect
											data-testid="daily-report-timezone"
											options={timezoneOptions}
											value={form.timezone}
											onValueChange={(timezone) => timezone && setForm({ ...form, timezone })}
											disabled={!permissions.canUpdate}
										/>
									</div>
									<div className="space-y-1.5">
										<Label>{copy.generateTime}</Label>
										<ReportTimeSelect
											testId="daily-report-generate-time"
											label={copy.generateTime}
											value={form.generate_time}
											onChange={(generate_time) => setForm({ ...form, generate_time })}
											disabled={!permissions.canUpdate}
										/>
									</div>
									<div className="space-y-1.5">
										<Label>{copy.sendTime}</Label>
										<ReportTimeSelect
											testId="daily-report-send-time"
											label={copy.sendTime}
											value={form.send_time}
											onChange={(send_time) => setForm({ ...form, send_time })}
											disabled={!permissions.canUpdate}
										/>
									</div>
									<div className="space-y-1.5 sm:col-span-2">
										<Label>{copy.slowThreshold}</Label>
										<div className="relative">
											<Input
												className="pr-12"
												type="number"
												min={0}
												value={form.slow_threshold_ms}
												onChange={(event) => setForm({ ...form, slow_threshold_ms: Number(event.target.value) })}
												disabled={!permissions.canUpdate}
											/>
											<span className="text-muted-foreground pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-xs">
												ms
											</span>
										</div>
										<p className="text-muted-foreground text-xs">{i18n.t("workspace.alerting.dailyReportsSlowThresholdDescription")}</p>
									</div>
								</div>
							</section>

							<aside className="p-5">
								<div className="mb-5 flex items-start gap-3">
									<CircleGauge className="text-muted-foreground mt-0.5 size-4" />
									<div>
										<h2 className="text-sm font-semibold">{i18n.t("workspace.alerting.dailyReportsScheduleState")}</h2>
										<p className="text-muted-foreground mt-1 text-xs">{i18n.t("workspace.alerting.dailyReportsMasterSwitchDescription")}</p>
									</div>
								</div>
								<label className="bg-muted/20 flex items-center justify-between gap-4 rounded-md border px-4 py-3">
									<span className="text-sm font-medium">{i18n.t("workspace.alerting.dailyReportsMasterSwitch")}</span>
									<Switch
										data-testid="daily-report-enabled"
										checked={form.enabled}
										onCheckedChange={(enabled) => setForm({ ...form, enabled })}
										disabled={!permissions.canUpdate}
									/>
								</label>
								<div className="mt-4 space-y-3 text-sm">
									<div className="flex justify-between gap-4">
										<span className="text-muted-foreground">{copy.generateTime}</span>
										<span className="font-medium tabular-nums">{form.generate_time}</span>
									</div>
									<div className="flex justify-between gap-4">
										<span className="text-muted-foreground">{copy.sendTime}</span>
										<span className="font-medium tabular-nums">{form.send_time}</span>
									</div>
									<div className="flex justify-between gap-4">
										<span className="text-muted-foreground">{copy.channels}</span>
										<span className="font-medium tabular-nums">{form.internal_channel_ids.length + form.external_channel_ids.length}</span>
									</div>
								</div>
							</aside>
						</div>
					</div>

					<section className="space-y-3">
						<div className="flex items-start gap-3 px-1">
							<UsersRound className="text-muted-foreground mt-0.5 size-4" />
							<div>
								<h2 className="text-sm font-semibold">{i18n.t("workspace.alerting.dailyReportsAudienceTitle")}</h2>
								<p className="text-muted-foreground mt-1 text-xs">{i18n.t("workspace.alerting.dailyReportsAudienceDescription")}</p>
							</div>
						</div>
						<div className="grid gap-3 lg:grid-cols-2">
							<AudienceChannels
								title={i18n.t("workspace.alerting.dailyReportsInternalAudience")}
								description={i18n.t("workspace.alerting.dailyReportsInternalAudienceDescription")}
								testId="daily-report-internal"
								enabled={form.internal_enabled}
								ids={form.internal_channel_ids}
								channels={channels}
								disabled={!permissions.canUpdate}
								onEnabled={(internal_enabled) => setForm({ ...form, internal_enabled })}
								onIDs={(internal_channel_ids) => setForm({ ...form, internal_channel_ids })}
							/>
							<AudienceChannels
								title={i18n.t("workspace.alerting.dailyReportsExternalAudience")}
								description={i18n.t("workspace.alerting.dailyReportsExternalAudienceDescription")}
								testId="daily-report-external"
								enabled={form.external_enabled}
								ids={form.external_channel_ids}
								channels={channels}
								disabled={!permissions.canUpdate}
								onEnabled={(external_enabled) => setForm({ ...form, external_enabled })}
								onIDs={(external_channel_ids) => setForm({ ...form, external_channel_ids })}
							/>
						</div>
					</section>
					{permissions.canUpdate ? (
						<div className="flex flex-wrap items-center justify-end gap-2 border-t pt-4">
							<Button
								variant="outline"
								disabled={!settingsData?.settings || saving.isLoading}
								onClick={() => settingsData?.settings && setForm(settingsToForm(settingsData.settings))}
								data-testid="daily-report-reset-settings"
							>
								<RotateCcw className="size-4" /> {i18n.t("workspace.alerting.dailyReportsReset")}
							</Button>
							<Button data-testid="daily-report-save-settings" disabled={saving.isLoading} onClick={save} isLoading={saving.isLoading}>
								<Save className="size-4" /> {copy.saveSettings}
							</Button>
						</div>
					) : null}
				</TabsContent>
				<TabsContent value="preview" className="space-y-4">
					<div className="flex flex-wrap items-end gap-2">
						<div>
							<Label>{copy.businessDate}</Label>
							<Input
								data-testid="daily-report-business-date"
								type="date"
								value={businessDate}
								onChange={(e) => setBusinessDate(e.target.value)}
							/>
						</div>
						{permissions.canPreview && (
							<Button
								data-testid="daily-report-preview"
								disabled={previewing.isLoading || jobActive}
								onClick={doPreview}
								isLoading={previewing.isLoading}
							>
								<Eye className="size-4" /> {copy.previewReport}
							</Button>
						)}
						{permissions.canGenerate && (
							<>
								<Button
									variant="outline"
									data-testid="daily-report-generate"
									disabled={jobActive || startingJob.isLoading}
									onClick={() => run(false)}
								>
									<Play className="size-4" /> {copy.generateReport}
								</Button>
								<Button
									data-testid="daily-report-generate-send"
									disabled={jobActive || startingJob.isLoading}
									onClick={() => run(true)}
									isLoading={startingJob.isLoading}
								>
									<Send className="size-4" /> {copy.generateAndSend}
								</Button>
							</>
						)}
					</div>
					{job && (
						<div className="space-y-3 rounded-sm border p-4" data-testid="daily-report-job-status" aria-live="polite">
							<div className="flex items-center justify-between gap-3">
								<div className="flex items-center gap-2">
									{job.status === "failed" && <CircleAlert className="text-destructive size-4" />}
									<span className="font-medium">{job.stage || job.status}</span>
									<Badge variant={job.status === "failed" ? "destructive" : "outline"}>{job.status}</Badge>
								</div>
								<span className="font-mono text-sm tabular-nums">{job.percent ?? 0}%</span>
							</div>
							<Progress value={job.percent ?? 0} />
							<p className={job.status === "failed" ? "text-destructive text-xs" : "text-muted-foreground text-xs"}>
								{job.last_error || job.message}
							</p>
						</div>
					)}
					{preview && <PreviewPanels preview={preview} />}
				</TabsContent>
				<TabsContent value="history">
					<DailyReportHistory canResend={permissions.canResend} refreshKey={historyRefreshKey} />
				</TabsContent>
			</Tabs>
		</div>
	);
}

function DailyReportHistory({ canResend, refreshKey }: { canResend: boolean; refreshKey: number }) {
	const [audience, setAudience] = useState<DailyReportAudience | "all">("all");
	const [offset, setOffset] = useState(0);
	const limit = 20;
	const { data, refetch } = useGetDailyReportHistoryQuery({ limit, offset, audience: audience === "all" ? undefined : [audience] });
	useEffect(() => {
		if (refreshKey > 0) void refetch();
	}, [refreshKey, refetch]);
	const [selectedID, setSelectedID] = useState<string | null>(null);
	const { data: detail } = useGetDailyReportRunQuery(selectedID ?? "", { skip: !selectedID });
	const [deliver] = useDeliverDailyReportRunMutation();
	const resend = async (run: DailyReportRunDetail, selectedAudience: DailyReportAudience) => {
		try {
			await deliver({ id: run.run.id, audience: [selectedAudience] }).unwrap();
			toast.success(copy.deliveryAttempted);
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	};
	return (
		<div className="space-y-4">
			<Select
				value={audience}
				onValueChange={(value) => {
					setAudience(value as DailyReportAudience | "all");
					setOffset(0);
				}}
			>
				<SelectTrigger className="w-48" data-testid="daily-report-history-audience">
					<SelectValue />
				</SelectTrigger>
				<SelectContent>
					<SelectItem value="all">{copy.allAudiences}</SelectItem>
					<SelectItem value="internal">{copy.internal}</SelectItem>
					<SelectItem value="external">{copy.external}</SelectItem>
				</SelectContent>
			</Select>
			{!data?.runs.length ? (
				<div className="text-muted-foreground rounded border border-dashed p-12 text-center">{copy.noReports}</div>
			) : (
				<Table>
					<TableHeader>
						<TableRow>
							<TableHead>{copy.businessDate}</TableHead>
							<TableHead>{copy.generatedAt}</TableHead>
							<TableHead>{copy.status}</TableHead>
							<TableHead>{copy.internal}</TableHead>
							<TableHead>{copy.external}</TableHead>
							<TableHead />
						</TableRow>
					</TableHeader>
					<TableBody>
						{data.runs.map((item) => (
							<TableRow key={item.run.id} data-testid="daily-report-history-row">
								<TableCell>{item.run.business_date}</TableCell>
								<TableCell>{new Date(item.run.generated_at).toLocaleString()}</TableCell>
								<TableCell>
									<Badge>{copy.dailyStatus[item.current_status]}</Badge>
								</TableCell>
								<TableCell>{copy.dailyStatus[item.current_internal_status]}</TableCell>
								<TableCell>{copy.dailyStatus[item.current_external_status]}</TableCell>
								<TableCell>
									<Button variant="ghost" size="sm" onClick={() => setSelectedID(item.run.id)}>
										{copy.viewDetails}
									</Button>
								</TableCell>
							</TableRow>
						))}
					</TableBody>
				</Table>
			)}
			<div className="flex justify-end gap-2">
				<Button variant="outline" size="icon" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - limit))}>
					<ChevronLeft className="size-4" />
				</Button>
				<Button variant="outline" size="icon" disabled={offset + limit >= (data?.total ?? 0)} onClick={() => setOffset(offset + limit)}>
					<ChevronRight className="size-4" />
				</Button>
			</div>
			<Dialog open={Boolean(selectedID)} onOpenChange={(open) => !open && setSelectedID(null)}>
				<DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-4xl">
					<DialogHeader>
						<DialogTitle>{detail?.run.business_date}</DialogTitle>
						<DialogDescription>{copy.dailyReportsDescription}</DialogDescription>
					</DialogHeader>
					{detail && (
						<>
							<PreviewPanels
								preview={{
									business_date: detail.run.business_date,
									snapshot: detail.run.snapshot,
									internal_content: detail.run.internal_content,
									external_content: detail.run.external_content,
								}}
							/>
							<div className="flex gap-2">
								{canResend && (
									<>
										<Button data-testid="daily-report-resend-internal" onClick={() => resend(detail, "internal")}>
											{copy.resendInternal}
										</Button>
										<Button data-testid="daily-report-resend-external" onClick={() => resend(detail, "external")}>
											{copy.resendExternal}
										</Button>
									</>
								)}
							</div>
							<div className="space-y-2">
								{detail.deliveries.map((delivery) => (
									<div key={delivery.id} className="flex justify-between rounded border p-2 text-sm">
										<span>
											{delivery.audience === "internal" ? copy.internal : copy.external} / {delivery.channel_name ?? delivery.channel_id} /
											#{delivery.attempt_no}
										</span>
										<Badge variant={delivery.status === "delivered" ? "default" : "destructive"}>{copy.dailyStatus[delivery.status]}</Badge>
									</div>
								))}
							</div>
						</>
					)}
				</DialogContent>
			</Dialog>
		</div>
	);
}