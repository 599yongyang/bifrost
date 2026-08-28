import type { DailyReportAudience, DailyReportAudienceStatus, DailyReportRunDetail } from "@/lib/types/alerting";

export type DailyReportAudienceSummary =
	| { kind: "status"; status: DailyReportAudienceStatus }
	| { kind: "deliveries"; delivered: number; failed: number; total: number };

export function summarizeDailyReportAudience(detail: DailyReportRunDetail, audience: DailyReportAudience): DailyReportAudienceSummary {
	const channelIDs = audience === "internal" ? detail.run.internal_channel_ids : detail.run.external_channel_ids;
	if (channelIDs.length === 0) return { kind: "status", status: "no_channels" };

	const latestByChannel = new Map<string, DailyReportRunDetail["deliveries"][number]>();
	for (const delivery of detail.deliveries) {
		if (delivery.audience !== audience) continue;
		const previous = latestByChannel.get(delivery.channel_id);
		if (!previous || delivery.attempt_no > previous.attempt_no) latestByChannel.set(delivery.channel_id, delivery);
	}
	if (latestByChannel.size === 0) {
		return {
			kind: "status",
			status: audience === "internal" ? detail.current_internal_status : detail.current_external_status,
		};
	}

	const latest = [...latestByChannel.values()];
	return {
		kind: "deliveries",
		delivered: latest.filter((delivery) => delivery.status === "delivered").length,
		failed: latest.filter((delivery) => delivery.status === "failed").length,
		total: channelIDs.length,
	};
}