import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import i18n from "@/lib/i18n";
import type { DimensionRankingsResponse } from "@/lib/types/logs";
import { formatCompactNumber } from "@/lib/utils/numbers";
import { memo, useMemo } from "react";

interface RoutingRuleStatsProps {
	data: DimensionRankingsResponse | null;
	loading: boolean;
	error: boolean;
}

function RoutingRuleStatsImpl({ data, loading, error }: RoutingRuleStatsProps) {
	const rules = useMemo(
		() => [...(data?.rankings ?? [])].sort((a, b) => b.total_requests - a.total_requests || (a.name || a.id).localeCompare(b.name || b.id)),
		[data],
	);

	return (
		<Card className="gap-4 py-4 shadow-none" data-testid="dashboard-routing-rule-stats">
			<CardHeader className="gap-1 px-4">
				<CardTitle className="text-sm">{i18n.t("workspace.dashboard.routingRuleStats")}</CardTitle>
				<CardDescription className="text-xs">{i18n.t("workspace.dashboard.routingRuleStatsDescription")}</CardDescription>
			</CardHeader>

			{loading ? (
				<div className="space-y-2 px-4" data-testid="dashboard-routing-rule-stats-loading">
					<Skeleton className="h-9 w-full" />
					<Skeleton className="h-9 w-full" />
					<Skeleton className="h-9 w-full" />
				</div>
			) : error ? (
				<div className="text-destructive flex min-h-28 items-center justify-center px-4 text-sm">
					{i18n.t("workspace.dashboard.routingRuleStatsError")}
				</div>
			) : rules.length === 0 ? (
				<div className="text-muted-foreground flex min-h-28 items-center justify-center px-4 text-sm">
					{i18n.t("workspace.dashboard.noRoutingRuleStats")}
				</div>
			) : (
				<div className="max-h-[360px] overflow-auto border-y">
					<Table>
						<TableHeader className="bg-card sticky top-0 z-10">
							<TableRow>
								<TableHead>{i18n.t("workspace.dashboard.routingRule")}</TableHead>
								<TableHead className="text-right">{i18n.t("workspace.dashboard.requests")}</TableHead>
								<TableHead className="text-right">{i18n.t("workspace.dashboard.successful")}</TableHead>
								<TableHead className="text-right">{i18n.t("workspace.dashboard.failed")}</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{rules.map((rule) => (
								<TableRow key={rule.id} data-testid={`dashboard-routing-rule-row-${rule.id}`}>
									<TableCell className="min-w-56">
										<div className="flex flex-col">
											<span className="max-w-[420px] truncate font-medium" title={rule.name || rule.id}>
												{rule.name || rule.id}
											</span>
											{rule.name && rule.name !== rule.id && <span className="text-muted-foreground font-mono text-xs">{rule.id}</span>}
										</div>
									</TableCell>
									<TableCell className="text-right font-mono tabular-nums" title={rule.total_requests.toLocaleString("en-US")}>
										{formatCompactNumber(rule.total_requests)}
									</TableCell>
									<TableCell
										className="text-right font-mono text-emerald-600 tabular-nums dark:text-emerald-400"
										title={rule.success_count.toLocaleString("en-US")}
									>
										{formatCompactNumber(rule.success_count)}
									</TableCell>
									<TableCell
										className="text-destructive text-right font-mono tabular-nums"
										title={rule.error_count.toLocaleString("en-US")}
									>
										{formatCompactNumber(rule.error_count)}
									</TableCell>
								</TableRow>
							))}
						</TableBody>
					</Table>
				</div>
			)}
		</Card>
	);
}

export const RoutingRuleStats = memo(RoutingRuleStatsImpl);