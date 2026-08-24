import { createFileRoute } from "@tanstack/react-router";
import { NoPermissionView } from "@/components/noPermissionView";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import AlertHistoryPage from "./page";

function AlertHistoryRoute() {
	const hasAccess = useRbac(RbacResource.AlertHistory, RbacOperation.View);
	return hasAccess ? <AlertHistoryPage /> : <NoPermissionView entity="alert history" />;
}

export const Route = createFileRoute("/workspace/alerting/history")({
	component: AlertHistoryRoute,
});