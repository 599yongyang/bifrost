import { localize } from "@/lib/i18n/language";
import type { BifrostError } from "@/lib/types/logs";

export interface TimeoutDetail {
	label: string;
	value: string;
}

function timeoutReason(source: string): string {
	switch (source) {
		case "bifrost_context_deadline":
		case "request_context_deadline":
			return localize("The request deadline was reached", "请求已达到截止时间");
		case "bifrost_http_client_timeout":
		case "configured_provider_timeout":
			return localize("The configured provider timeout was reached", "已达到配置的供应商超时时间");
		case "upstream_connection_timeout":
			return localize("The upstream connection or proxy timed out before returning a response", "上游连接或代理在返回响应前超时");
		case "upstream_connection_error":
			return localize("The upstream connection or proxy disconnected before returning a response", "上游连接或代理在返回响应前断开");
		case "upstream_http_504":
			return localize("The upstream returned HTTP 504 Gateway Timeout", "上游返回 HTTP 504 网关超时");
		default:
			return localize("The timeout source could not be determined", "无法确定超时来源");
	}
}

export function getTimeoutDetails(error?: BifrostError): TimeoutDetail[] {
	const fields = error?.extra_fields;
	if (!fields?.timeout_source) return [];

	const details: TimeoutDetail[] = [
		{ label: localize("Reason", "原因"), value: timeoutReason(fields.timeout_source) },
		{ label: localize("Source", "来源"), value: fields.timeout_source },
	];
	if (typeof fields.elapsed_ms === "number") {
		details.push({
			label: localize("Elapsed", "已耗时"),
			value: `${(fields.elapsed_ms / 1000).toFixed(2)} s (${fields.elapsed_ms} ms)`,
		});
	}
	if (typeof fields.configured_timeout_seconds === "number" && fields.configured_timeout_seconds > 0) {
		details.push({
			label: localize("Configured timeout", "配置超时"),
			value: `${fields.configured_timeout_seconds} s`,
		});
	}
	if (typeof fields.upstream_response_received === "boolean") {
		details.push({
			label: localize("Upstream response received", "已收到上游响应"),
			value: fields.upstream_response_received ? localize("Yes", "是") : localize("No", "否"),
		});
	}
	return details;
}