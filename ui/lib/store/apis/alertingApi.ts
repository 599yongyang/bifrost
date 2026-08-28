import type {
	AlertChannel,
	AlertChannelRequest,
	AlertHistoryParams,
	AlertHistoryRecord,
	AlertRule,
	AlertRuleEvaluationResult,
	AlertRuleRequest,
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
			transformResponse: (r: { channel: AlertChannel }) => r.channel,
			invalidatesTags: ["AlertChannels"],
		}),
		updateAlertChannel: builder.mutation<AlertChannel, { id: string; data: AlertChannelRequest }>({
			query: ({ id, data }) => ({ url: `/alerting/channels/${id}`, method: "PUT", body: data }),
			transformResponse: (r: { channel: AlertChannel }) => r.channel,
			invalidatesTags: ["AlertChannels", "AlertRules"],
		}),
		deleteAlertChannel: builder.mutation<void, string>({
			query: (id) => ({ url: `/alerting/channels/${id}`, method: "DELETE" }),
			invalidatesTags: ["AlertChannels", "AlertRules"],
		}),
		testAlertChannel: builder.mutation<void, string>({ query: (id) => ({ url: `/alerting/channels/${id}/test`, method: "POST" }) }),
		getAlertRules: builder.query<{ rules: AlertRule[]; count: number }, void>({
			query: () => "/alerting/rules",
			providesTags: ["AlertRules"],
		}),
		createAlertRule: builder.mutation<AlertRule, AlertRuleRequest>({
			query: (body) => ({ url: "/alerting/rules", method: "POST", body }),
			transformResponse: (r: { rule: AlertRule }) => r.rule,
			invalidatesTags: ["AlertRules"],
		}),
		updateAlertRule: builder.mutation<AlertRule, { id: string; data: AlertRuleRequest }>({
			query: ({ id, data }) => ({ url: `/alerting/rules/${id}`, method: "PUT", body: data }),
			transformResponse: (r: { rule: AlertRule }) => r.rule,
			invalidatesTags: ["AlertRules"],
		}),
		deleteAlertRule: builder.mutation<void, string>({
			query: (id) => ({ url: `/alerting/rules/${id}`, method: "DELETE" }),
			invalidatesTags: ["AlertRules"],
		}),
		getAlertRuleEvaluationStatus: builder.query<{ running_rule_ids: string[] }, void>({ query: () => "/alerting/rules/evaluation-status" }),
		evaluateAlertRule: builder.mutation<AlertRuleEvaluationResult, { id: string; ignoreCooldown: boolean }>({
			query: ({ id, ignoreCooldown }) => ({
				url: `/alerting/rules/${id}/evaluate`,
				method: "POST",
				body: { ignore_cooldown: ignoreCooldown },
			}),
			transformResponse: (r: { result: AlertRuleEvaluationResult }) => r.result,
			invalidatesTags: ["AlertHistory"],
		}),
		evaluateAlerts: builder.mutation<void, void>({
			query: () => ({ url: "/alerting/evaluate", method: "POST" }),
			invalidatesTags: ["AlertHistory"],
		}),
		getAlertHistory: builder.query<
			{ history: AlertHistoryRecord[]; total: number; limit: number; offset: number },
			AlertHistoryParams | void
		>({
			query: (p) => ({
				url: "/alerting/history",
				params: {
					limit: p?.limit,
					offset: p?.offset,
					status: p?.status?.join(","),
					scope_type: p?.scope_type?.join(","),
					channel_type: p?.channel_type?.join(","),
				},
			}),
			providesTags: ["AlertHistory"],
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
	useEvaluateAlertsMutation,
	useGetAlertHistoryQuery,
} = alertingApi;