import { CheckCircle2, CircleAlert } from "lucide-react";
import i18n from "@/lib/i18n";

export default function AgentHandoverView() {
	const status = new URLSearchParams(window.location.search).get("status");
	const isComplete = !status || status === "complete";
	const Icon = isComplete ? CheckCircle2 : CircleAlert;

	return (
		<main className="bg-background text-foreground flex min-h-screen items-center justify-center p-6">
			<section className="bg-card w-full max-w-xl rounded-sm border p-8 text-center shadow-sm">
				<div className="bg-primary/10 mx-auto mb-5 flex size-12 items-center justify-center rounded-full">
					<Icon className="text-primary size-6" />
				</div>
				<h1 className="text-xl font-semibold tracking-tight">
					{isComplete ? i18n.t("workspace.enterpriseFallbacks.agentCompleteTitle") : i18n.t("workspace.enterpriseFallbacks.agentTitle")}
				</h1>
				<p className="text-muted-foreground mt-2 text-sm">
					{isComplete
						? i18n.t("workspace.enterpriseFallbacks.agentCompleteDescription")
						: i18n.t("workspace.enterpriseFallbacks.agentStatus", { status })}
				</p>
			</section>
		</main>
	);
}