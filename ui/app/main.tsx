// Keep the synchronous entry tiny: load only the active locale, then evaluate
// application modules whose top-level copy may read the initialized language.
import "@/app/globals.css";
import { i18nReady } from "@/lib/i18n";
import { getBifrostLanguage } from "@/lib/i18n/language";
import { consumeAutoReload, installVersionSkewListeners, isSkewError } from "@/lib/utils/versionSkew";

installVersionSkewListeners();

function renderBootstrapFailure(error: unknown) {
	const root = document.getElementById("root");
	if (!root) return;
	const chinese = getBifrostLanguage() === "zh";
	const container = document.createElement("main");
	container.className = "flex min-h-screen items-center justify-center bg-background p-6 text-foreground";
	const panel = document.createElement("section");
	panel.className = "w-full max-w-lg rounded-md border bg-card p-6 text-center shadow-sm";
	const title = document.createElement("h1");
	title.className = "text-lg font-semibold";
	title.textContent = chinese ? "界面资源加载失败" : "Unable to load the dashboard";
	const description = document.createElement("p");
	description.className = "mt-2 text-sm text-muted-foreground";
	description.textContent = chinese
		? "请检查网络连接并重试。你的设置和数据不会受到影响。"
		: "Check your network connection and try again. Your settings and data are unaffected.";
	const button = document.createElement("button");
	button.type = "button";
	button.className = "mt-5 rounded-md border px-4 py-2 text-sm font-medium hover:bg-accent";
	button.textContent = chinese ? "重新加载" : "Reload";
	button.addEventListener("click", () => window.location.reload());
	panel.append(title, description, button);
	container.append(panel);
	root.replaceChildren(container);
	console.error("Failed to bootstrap Bifrost UI", error);
}

async function bootstrap() {
	try {
		await i18nReady;
		await import("./bootstrap");
	} catch (error) {
		if (isSkewError(error) && consumeAutoReload()) {
			window.location.reload();
			return;
		}
		renderBootstrapFailure(error);
	}
}

void bootstrap();