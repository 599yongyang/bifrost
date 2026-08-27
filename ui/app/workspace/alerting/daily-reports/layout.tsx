import { createFileRoute } from "@tanstack/react-router";
import { NoPermissionView } from "@/components/noPermissionView";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import DailyReportsPage from "./page";

function DailyReportsRoute() {
	const hasRulesAccess = useRbac(RbacResource.AlertRules, RbacOperation.View);
	return hasRulesAccess ? <DailyReportsPage /> : <NoPermissionView entity="daily reports" />;
}

export const Route = createFileRoute("/workspace/alerting/daily-reports")({
	component: DailyReportsRoute,
});