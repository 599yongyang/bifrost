import { afterEach, describe, expect, it } from "vitest";

import type { BifrostError } from "@/lib/types/logs";
import { getTimeoutDetails } from "./timeoutDetails";

const originalDocument = Object.getOwnPropertyDescriptor(globalThis, "document");

afterEach(() => {
	if (originalDocument) Object.defineProperty(globalThis, "document", originalDocument);
	else Reflect.deleteProperty(globalThis, "document");
});

describe("getTimeoutDetails", () => {
	it("shows upstream timeout evidence without attributing it to the configured timeout", () => {
		const error: BifrostError = {
			is_bifrost_error: false,
			error: { message: "upstream connection or proxy timed out" },
			extra_fields: {
				timeout_source: "upstream_connection_timeout",
				configured_timeout_seconds: 600,
				elapsed_ms: 27_000,
				upstream_response_received: false,
			},
		};

		const rendered = getTimeoutDetails(error)
			.map(({ label, value }) => `${label}: ${value}`)
			.join("\n");
		expect(rendered).toContain("upstream connection or proxy timed out");
		expect(rendered).toContain("27.00 s (27000 ms)");
		expect(rendered).toContain("Configured timeout: 600 s");
		expect(rendered).toContain("Upstream response received: No");
	});

	it("supports the public configured-provider timeout source", () => {
		const error: BifrostError = {
			is_bifrost_error: false,
			error: { message: "provider request reached the configured timeout" },
			extra_fields: { timeout_source: "configured_provider_timeout", configured_timeout_seconds: 30 },
		};

		expect(
			getTimeoutDetails(error)
				.map(({ value }) => value)
				.join(" "),
		).toContain("configured provider timeout was reached");
	});

	it("localizes timeout evidence in Chinese", () => {
		Object.defineProperty(globalThis, "document", { configurable: true, value: { documentElement: { lang: "zh" } } });
		const error: BifrostError = {
			is_bifrost_error: false,
			error: { message: "timeout" },
			extra_fields: { timeout_source: "upstream_http_504", upstream_response_received: true },
		};

		const rendered = getTimeoutDetails(error)
			.map(({ label, value }) => `${label}: ${value}`)
			.join("\n");
		expect(rendered).toContain("原因: 上游返回 HTTP 504 网关超时");
		expect(rendered).toContain("已收到上游响应: 是");
	});
});