import { createFileRoute, redirect } from "@tanstack/react-router";

export const Route = createFileRoute("/workspace/alerting/daily-reports")({
	beforeLoad: () => {
		throw redirect({ to: "/workspace/alerting/reports", replace: true });
	},
});