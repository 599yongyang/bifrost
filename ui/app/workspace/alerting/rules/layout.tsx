import { createFileRoute } from "@tanstack/react-router";
import { NoPermissionView } from "@/components/noPermissionView";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import AlertRulesPage from "./page";

function AlertRulesRoute() {
	const hasAccess = useRbac(RbacResource.AlertRules, RbacOperation.View);
	return hasAccess ? <AlertRulesPage /> : <NoPermissionView entity="alert rules" />;
}

export const Route = createFileRoute("/workspace/alerting/rules")({
	component: AlertRulesRoute,
});