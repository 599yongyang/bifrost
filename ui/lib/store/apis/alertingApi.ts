import {
	AlertChannel,
	AlertChannelRequest,
	AlertHistoryParams,
	AlertHistoryRecord,
	AlertRule,
	AlertRuleEvaluationResult,
	AlertRuleRequest,
	DailyReportAudience,
	DailyReportDelivery,
	DailyReportPreview,
	DailyReportHistoryParams,
	DailyReportRunDetail,
	DailyReportSettings,
	DailyReportSettingsRequest,
	DailyReportJobStatus,
} from "@/lib/types/alerting";
import { baseApi } from "./baseApi";

export const alertingApi = baseApi.injectEndpoints({
	endpoints: (builder) => ({
		getAlertChannels: builder.query<{ channels: AlertChannel[]; count: number }, void>({
			query: () => "/alerting/channels",
			providesTags: ["AlertChannels"],
		}),
		createAlertChannel: builder.mutation<AlertChannel, AlertChannelRequest>({
			query: (body) => ({ url: "/alerting/channels", method: "POST", body }),
			transformResponse: (response: { channel: AlertChannel }) => response.channel,
			invalidatesTags: ["AlertChannels"],
		}),
		updateAlertChannel: builder.mutation<AlertChannel, { id: string; data: AlertChannelRequest }>({
			query: ({ id, data }) => ({ url: `/alerting/channels/${id}`, method: "PUT", body: data }),
			transformResponse: (response: { channel: AlertChannel }) => response.channel,
			invalidatesTags: ["AlertChannels", "AlertRules"],
		}),
		deleteAlertChannel: builder.mutation<void, string>({
			query: (id) => ({ url: `/alerting/channels/${id}`, method: "DELETE" }),
			invalidatesTags: ["AlertChannels", "AlertRules"],
		}),
		testAlertChannel: builder.mutation<void, string>({
			query: (id) => ({ url: `/alerting/channels/${id}/test`, method: "POST" }),
		}),

		getAlertRules: builder.query<{ rules: AlertRule[]; count: number }, void>({
			query: () => "/alerting/rules",
			providesTags: ["AlertRules"],
		}),
		createAlertRule: builder.mutation<AlertRule, AlertRuleRequest>({
			query: (body) => ({ url: "/alerting/rules", method: "POST", body }),
			transformResponse: (response: { rule: AlertRule }) => response.rule,
			invalidatesTags: ["AlertRules"],
		}),
		updateAlertRule: builder.mutation<AlertRule, { id: string; data: AlertRuleRequest }>({
			query: ({ id, data }) => ({ url: `/alerting/rules/${id}`, method: "PUT", body: data }),
			transformResponse: (response: { rule: AlertRule }) => response.rule,
			invalidatesTags: ["AlertRules"],
		}),
		deleteAlertRule: builder.mutation<void, string>({
			query: (id) => ({ url: `/alerting/rules/${id}`, method: "DELETE" }),
			invalidatesTags: ["AlertRules"],
		}),
		getAlertRuleEvaluationStatus: builder.query<{ running_rule_ids: string[] }, void>({
			query: () => "/alerting/rules/evaluation-status",
		}),
		evaluateAlertRule: builder.mutation<AlertRuleEvaluationResult, { id: string; ignoreCooldown: boolean }>({
			query: ({ id, ignoreCooldown }) => ({
				url: `/alerting/rules/${id}/evaluate`,
				method: "POST",
				body: { ignore_cooldown: ignoreCooldown },
			}),
			transformResponse: (response: { result: AlertRuleEvaluationResult }) => response.result,
			invalidatesTags: ["AlertHistory"],
		}),

		getAlertHistory: builder.query<
			{ history: AlertHistoryRecord[]; total: number; limit: number; offset: number },
			AlertHistoryParams | void
		>({
			query: (params) => ({
				url: "/alerting/history",
				params: {
					limit: params?.limit,
					offset: params?.offset,
					status: params?.status?.join(","),
					scope_type: params?.scope_type?.join(","),
					channel_type: params?.channel_type?.join(","),
				},
			}),
			providesTags: ["AlertHistory"],
		}),

		getDailyReportSettings: builder.query<{ settings: DailyReportSettings }, void>({
			query: () => "/alerting/reports/settings",
			providesTags: ["DailyReports"],
		}),
		updateDailyReportSettings: builder.mutation<DailyReportSettings, DailyReportSettingsRequest>({
			query: (body) => ({ url: "/alerting/reports/settings", method: "PUT", body }),
			transformResponse: (response: { settings: DailyReportSettings }) => response.settings,
			invalidatesTags: ["DailyReports"],
		}),
		previewDailyReport: builder.mutation<DailyReportPreview, { business_date?: string; settings?: Partial<DailyReportSettingsRequest> }>({
			query: (body) => ({ url: "/alerting/reports/preview", method: "POST", body }),
			transformResponse: (response: { preview: DailyReportPreview }) => response.preview,
		}),
		startDailyReportJob: builder.mutation<
			DailyReportJobStatus,
			{ business_date?: string; deliver?: boolean; settings?: Partial<DailyReportSettingsRequest> }
		>({
			query: (body) => ({ url: "/alerting/reports/jobs", method: "POST", body }),
			invalidatesTags: ["DailyReports"],
		}),
		getDailyReportJobStatus: builder.query<DailyReportJobStatus, { id?: string } | void>({
			query: (params) => ({ url: "/alerting/reports/jobs/status", params: { id: params?.id } }),
			providesTags: ["DailyReports"],
		}),
		sendDailyReportNow: builder.mutation<DailyReportRunDetail, { business_date?: string }>({
			query: (body) => ({ url: "/alerting/reports/generate", method: "POST", body }),
			transformResponse: (response: { result: DailyReportRunDetail }) => response.result,
			invalidatesTags: ["DailyReports"],
		}),
		getDailyReportHistory: builder.query<
			{ runs: DailyReportRunDetail[]; total: number; limit: number; offset: number },
			DailyReportHistoryParams | void
		>({
			query: (params) => ({
				url: "/alerting/reports/runs",
				params: {
					limit: params?.limit,
					offset: params?.offset,
					audience: params?.audience?.join(","),
				},
			}),
			providesTags: ["DailyReports"],
		}),
		getDailyReportRun: builder.query<DailyReportRunDetail, string>({
			query: (id) => `/alerting/reports/runs/${id}`,
			transformResponse: (response: { run: DailyReportRunDetail }) => response.run,
			providesTags: ["DailyReports"],
		}),
		deliverDailyReportRun: builder.mutation<DailyReportRunDetail, { id: string; audience: DailyReportAudience[] }>({
			query: ({ id, audience }) => ({
				url: `/alerting/reports/runs/${id}/deliver`,
				method: "POST",
				body: { audience },
			}),
			transformResponse: (response: { run: DailyReportRunDetail }) => response.run,
			invalidatesTags: ["DailyReports"],
		}),
	}),
});

export const {
	useGetAlertChannelsQuery,
	useCreateAlertChannelMutation,
	useUpdateAlertChannelMutation,
	useDeleteAlertChannelMutation,
	useTestAlertChannelMutation,
	useGetAlertRulesQuery,
	useCreateAlertRuleMutation,
	useUpdateAlertRuleMutation,
	useDeleteAlertRuleMutation,
	useGetAlertRuleEvaluationStatusQuery,
	useEvaluateAlertRuleMutation,
	useGetAlertHistoryQuery,
	useGetDailyReportSettingsQuery,
	useUpdateDailyReportSettingsMutation,
	usePreviewDailyReportMutation,
	useStartDailyReportJobMutation,
	useGetDailyReportJobStatusQuery,
	useSendDailyReportNowMutation,
	useGetDailyReportHistoryQuery,
	useGetDailyReportRunQuery,
	useDeliverDailyReportRunMutation,
} = alertingApi;