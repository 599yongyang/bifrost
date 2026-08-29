import i18n from "@/lib/i18n";
import type { BifrostError } from "@/lib/types/logs";

export interface TimeoutDetail {
	label: string;
	value: string;
}

function timeoutReason(source: string): string {
	switch (source) {
		case "bifrost_context_deadline":
		case "request_context_deadline":
			return i18n.t("workspace.logs.copy.timeoutDetails_the_request_deadline_was_reached");
		case "bifrost_http_client_timeout":
		case "configured_provider_timeout":
			return i18n.t("workspace.logs.copy.timeoutDetails_the_configured_provider_timeout_was_reached");
		case "upstream_connection_timeout":
			return i18n.t("workspace.logs.copy.timeoutDetails_the_upstream_connection_or_proxy_timed_out_before_return");
		case "upstream_connection_error":
			return i18n.t("workspace.logs.copy.timeoutDetails_the_upstream_connection_or_proxy_disconnected_before_ret");
		case "upstream_http_504":
			return i18n.t("workspace.logs.copy.timeoutDetails_the_upstream_returned_http_504_gateway_timeout");
		default:
			return i18n.t("workspace.logs.copy.timeoutDetails_the_timeout_source_could_not_be_determined");
	}
}

export function getTimeoutDetails(error?: BifrostError): TimeoutDetail[] {
	const fields = error?.extra_fields;
	if (!fields?.timeout_source) return [];

	const details: TimeoutDetail[] = [
		{ label: i18n.t("workspace.logs.copy.timeoutDetails_reason"), value: timeoutReason(fields.timeout_source) },
		{ label: i18n.t("workspace.logs.copy.timeoutDetails_source"), value: fields.timeout_source },
	];
	if (typeof fields.elapsed_ms === "number") {
		details.push({
			label: i18n.t("workspace.logs.copy.timeoutDetails_elapsed"),
			value: `${(fields.elapsed_ms / 1000).toFixed(2)} s (${fields.elapsed_ms} ms)`,
		});
	}
	if (typeof fields.configured_timeout_seconds === "number" && fields.configured_timeout_seconds > 0) {
		details.push({
			label: i18n.t("workspace.logs.copy.timeoutDetails_configured_timeout"),
			value: `${fields.configured_timeout_seconds} s`,
		});
	}
	if (typeof fields.upstream_response_received === "boolean") {
		details.push({
			label: i18n.t("workspace.logs.copy.timeoutDetails_upstream_response_received"),
			value: fields.upstream_response_received
				? i18n.t("workspace.logs.copy.timeoutDetails_yes")
				: i18n.t("workspace.logs.copy.timeoutDetails_no"),
		});
	}
	return details;
}