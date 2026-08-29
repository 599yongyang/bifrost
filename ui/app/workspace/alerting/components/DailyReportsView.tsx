import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
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
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { ChevronLeft, ChevronRight, Eye, Play, RefreshCw, Save, Send } from "lucide-react";
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
	enabled,
	ids,
	channels,
	disabled,
	onEnabled,
	onIDs,
}: {
	title: string;
	enabled: boolean;
	ids: string[];
	channels: { id: string; name: string; enabled: boolean; type: string }[];
	disabled: boolean;
	onEnabled: (value: boolean) => void;
	onIDs: (ids: string[]) => void;
}) {
	return (
		<div className="space-y-3 rounded-sm border p-4">
			<div className="flex items-center justify-between">
				<Label>{title}</Label>
				<Switch checked={enabled} onCheckedChange={onEnabled} disabled={disabled} />
			</div>
			{enabled && (
				<div className="grid gap-2 sm:grid-cols-2">
					{channels
						.filter((channel) => channel.enabled && channel.type !== "pagerduty")
						.map((channel) => (
							<label key={channel.id} className="flex items-center gap-2 rounded border p-2">
								<input
									type="checkbox"
									disabled={disabled}
									checked={ids.includes(channel.id)}
									onChange={(event) => onIDs(event.target.checked ? [...ids, channel.id] : ids.filter((id) => id !== channel.id))}
								/>
								{channel.name}
							</label>
						))}
				</div>
			)}
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
	const [startJob] = useStartDailyReportJobMutation();
	const { data: jobStatus } = useGetDailyReportJobStatusQuery(job?.id ? { id: job.id } : undefined, {
		skip: !job?.id,
		pollingInterval: shouldPollDailyReportJob(job ?? undefined) ? 1500 : 0,
	});
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
		<div className="space-y-5" data-testid="daily-reports-view">
			<div>
				<h1 className="text-lg font-semibold">
					{copy.dailyReports} <Badge variant="outline">{copy.beta}</Badge>
				</h1>
				<p className="text-muted-foreground text-sm">{copy.dailyReportsDescription}</p>
			</div>
			<Tabs defaultValue="settings">
				<TabsList>
					<TabsTrigger value="settings">{copy.settings}</TabsTrigger>
					<TabsTrigger value="preview">{copy.preview}</TabsTrigger>
					<TabsTrigger value="history">{copy.reportHistory}</TabsTrigger>
				</TabsList>
				<TabsContent value="settings" className="space-y-4">
					<div className="grid gap-4 md:grid-cols-2">
						<label className="flex items-center justify-between rounded border p-3">
							<span>{copy.enabled}</span>
							<Switch
								data-testid="daily-report-enabled"
								checked={form.enabled}
								onCheckedChange={(enabled) => setForm({ ...form, enabled })}
								disabled={!permissions.canUpdate}
							/>
						</label>
						<div>
							<Label>{copy.timezone}</Label>
							<Input
								data-testid="daily-report-timezone"
								value={form.timezone}
								onChange={(e) => setForm({ ...form, timezone: e.target.value })}
								disabled={!permissions.canUpdate}
							/>
						</div>
						<div>
							<Label>{copy.generateTime}</Label>
							<Input
								type="time"
								value={form.generate_time}
								onChange={(e) => setForm({ ...form, generate_time: e.target.value })}
								disabled={!permissions.canUpdate}
							/>
						</div>
						<div>
							<Label>{copy.sendTime}</Label>
							<Input
								type="time"
								value={form.send_time}
								onChange={(e) => setForm({ ...form, send_time: e.target.value })}
								disabled={!permissions.canUpdate}
							/>
						</div>
						<div>
							<Label>{copy.slowThreshold}</Label>
							<Input
								type="number"
								min={0}
								value={form.slow_threshold_ms}
								onChange={(e) => setForm({ ...form, slow_threshold_ms: Number(e.target.value) })}
								disabled={!permissions.canUpdate}
							/>
						</div>
					</div>
					<AudienceChannels
						title={copy.internalAudience}
						enabled={form.internal_enabled}
						ids={form.internal_channel_ids}
						channels={channels}
						disabled={!permissions.canUpdate}
						onEnabled={(internal_enabled) => setForm({ ...form, internal_enabled })}
						onIDs={(internal_channel_ids) => setForm({ ...form, internal_channel_ids })}
					/>
					<AudienceChannels
						title={copy.externalAudience}
						enabled={form.external_enabled}
						ids={form.external_channel_ids}
						channels={channels}
						disabled={!permissions.canUpdate}
						onEnabled={(external_enabled) => setForm({ ...form, external_enabled })}
						onIDs={(external_channel_ids) => setForm({ ...form, external_channel_ids })}
					/>
					{permissions.canUpdate && (
						<Button data-testid="daily-report-save-settings" disabled={saving.isLoading} onClick={save}>
							<Save className="size-4" /> {copy.saveSettings}
						</Button>
					)}
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
							<Button data-testid="daily-report-preview" disabled={previewing.isLoading} onClick={doPreview}>
								<Eye className="size-4" /> {copy.previewReport}
							</Button>
						)}
						{permissions.canGenerate && (
							<>
								<Button variant="outline" data-testid="daily-report-generate" onClick={() => run(false)}>
									<Play className="size-4" /> {copy.generateReport}
								</Button>
								<Button data-testid="daily-report-generate-send" onClick={() => run(true)}>
									<Send className="size-4" /> {copy.generateAndSend}
								</Button>
							</>
						)}
					</div>
					{job && (
						<div className="space-y-2 rounded border p-3" data-testid="daily-report-job-status">
							<div className="flex justify-between">
								<span>{job.stage || job.status}</span>
								<span>{job.percent ?? 0}%</span>
							</div>
							<Progress value={job.percent ?? 0} />
							<p className="text-muted-foreground text-xs">{job.message}</p>
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
				<DialogContent className="max-h-[90vh] max-w-4xl overflow-y-auto">
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