import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { LogEntry } from "@/lib/types/logs";
import { cn } from "@/lib/utils";
import { format } from "date-fns";
import { CircleAlert, CircleCheck, CircleOff, Clock3, CloudUpload } from "lucide-react";
import i18n from "@/lib/i18n";
import { observationReasonGuidance, observationReasonLabel, observationStatusLabel } from "../utils/observabilityCopy";
import { resolveObservabilityExport } from "../utils/observabilityExport";

interface ObservabilityExportPanelProps {
	log: LogEntry;
	onRetry?: (log: LogEntry) => void;
}

const formatTimestamp = (value?: string) => {
	if (!value) return "—";
	const date = new Date(value);
	return Number.isNaN(date.getTime()) ? "—" : format(date, "yyyy-MM-dd HH:mm:ss");
};

export function ObservabilityExportPanel({ log, onRetry }: ObservabilityExportPanelProps) {
	const resolved = resolveObservabilityExport(log);
	const state = resolved.state;
	if (!log.observability_export_configured && !state) return null;

	const reason = observationReasonLabel(state?.reason);
	const guidance = observationReasonGuidance(state?.reason);
	const statusConfig = {
		exported: { icon: CircleCheck, badge: "default" as const, tone: "border-emerald-200 bg-emerald-50/60 dark:border-emerald-900 dark:bg-emerald-950/20" },
		pending: { icon: Clock3, badge: "secondary" as const, tone: "border-blue-200 bg-blue-50/60 dark:border-blue-900 dark:bg-blue-950/20" },
		failed: { icon: CircleAlert, badge: "destructive" as const, tone: "border-red-200 bg-red-50/60 dark:border-red-900 dark:bg-red-950/20" },
		unavailable: { icon: CircleOff, badge: "outline" as const, tone: "border-amber-200 bg-amber-50/60 dark:border-amber-900 dark:bg-amber-950/20" },
		not_exported: { icon: CircleOff, badge: "outline" as const, tone: "bg-muted/20" },
		unknown: { icon: CircleOff, badge: "outline" as const, tone: "bg-muted/20" },
	}[resolved.status];
	const StatusIcon = statusConfig.icon;

	return (
		<section
			className={cn("rounded-sm border px-4 py-3", statusConfig.tone)}
			data-testid="log-observability-export-details"
			aria-label={i18n.t("workspace.logs.observability.detailsTitle")}
		>
			<div className="flex flex-wrap items-start justify-between gap-3">
				<div className="flex min-w-0 items-start gap-3">
					<StatusIcon className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
					<div className="min-w-0">
						<div className="flex flex-wrap items-center gap-2">
							<h3 className="text-sm font-semibold">{i18n.t("workspace.logs.observability.detailsTitle")}</h3>
							<Badge variant={statusConfig.badge}>{observationStatusLabel(resolved.status)}</Badge>
						</div>
						<p className="text-muted-foreground mt-1 text-sm">
							{reason || i18n.t("workspace.logs.observability.notEvaluatedHelp")}
						</p>
						{guidance ? <p className="text-foreground/80 mt-1 text-xs leading-relaxed">{guidance}</p> : null}
					</div>
				</div>
				{resolved.canManualExport && onRetry ? (
					<Button type="button" size="sm" variant="outline" onClick={() => onRetry(log)} data-testid="log-detail-observability-retry-btn">
						<CloudUpload className="size-4" />
						{state ? i18n.t("workspace.logs.observability.retry") : i18n.t("workspace.logs.observability.export")}
					</Button>
				) : null}
			</div>

			{state ? (
				<dl className="mt-3 grid grid-cols-2 gap-x-5 gap-y-2 border-t border-current/10 pt-3 text-xs md:grid-cols-4">
					<div>
						<dt className="text-muted-foreground">{i18n.t("workspace.logs.observability.source")}</dt>
						<dd className="mt-0.5 font-medium">
							{state.source === "manual" ? i18n.t("workspace.logs.observability.manual") : i18n.t("workspace.logs.observability.automatic")}
						</dd>
					</div>
					<div>
						<dt className="text-muted-foreground">{i18n.t("workspace.logs.observability.attempts")}</dt>
						<dd className="mt-0.5 font-mono font-medium tabular-nums">{state.attempts}</dd>
					</div>
					<div>
						<dt className="text-muted-foreground">{i18n.t("workspace.logs.observability.lastAttempt")}</dt>
						<dd className="mt-0.5 font-mono">{formatTimestamp(state.updated_at)}</dd>
					</div>
					{state.exported_at ? (
						<div>
							<dt className="text-muted-foreground">{i18n.t("workspace.logs.observability.exportedAt")}</dt>
							<dd className="mt-0.5 font-mono">{formatTimestamp(state.exported_at)}</dd>
						</div>
					) : null}
					{state.selection_rule ? (
						<div>
							<dt className="text-muted-foreground">{i18n.t("workspace.logs.observability.matchingPolicy")}</dt>
							<dd className="mt-0.5 font-mono">{state.selection_rule}</dd>
						</div>
					) : null}
					{state.external_trace_id ? (
						<div className="col-span-2">
							<dt className="text-muted-foreground">{i18n.t("workspace.logs.observability.traceId")}</dt>
							<dd className="mt-0.5 truncate font-mono" title={state.external_trace_id}>
								{state.external_trace_id}
							</dd>
						</div>
					) : null}
				</dl>
			) : null}
		</section>
	);
}
