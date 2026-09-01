import { describe, expect, it } from "vitest";
import { getDefaultPerformanceConfig, isKeyRequiredByProvider, ModelPlaceholders } from "./config";
import { ProviderLabels, ProviderNames } from "./logs";

describe("APIMart provider configuration", () => {
	it("is registered with key and model configuration", () => {
		expect(ProviderNames).toContain("apimart");
		expect(ProviderLabels.apimart).toBe("APIMart");
		expect(isKeyRequiredByProvider.apimart).toBe(true);
		expect(ModelPlaceholders.apimart).toContain("gpt-image-2");
	});

	it("uses bounded defaults for long-running async tasks", () => {
		expect(getDefaultPerformanceConfig("apimart")).toEqual({ concurrency: 32, buffer_size: 128 });
		expect(getDefaultPerformanceConfig("openai")).toEqual({ concurrency: 1000, buffer_size: 5000 });
	});
});