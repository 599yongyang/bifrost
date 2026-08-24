import { createFileRoute, Outlet, useLocation, useNavigate } from "@tanstack/react-router";
import { NoPermissionView } from "@/components/noPermissionView";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { useEffect } from "react";

function RouteComponent() {
	const hasRulesAccess = useRbac(RbacResource.AlertRules, RbacOperation.View);
	const hasChannelsAccess = useRbac(RbacResource.AlertChannels, RbacOperation.View);
	const hasHistoryAccess = useRbac(RbacResource.AlertHistory, RbacOperation.View);
	const hasAlertingAccess = hasRulesAccess || hasChannelsAccess || hasHistoryAccess;
	const location = useLocation();
	const navigate = useNavigate();
	const isIndex = location.pathname === "/workspace/alerting" || location.pathname === "/workspace/alerting/";
	const defaultRoute = hasRulesAccess
		? "/workspace/alerting/rules"
		: hasChannelsAccess
			? "/workspace/alerting/channels"
			: "/workspace/alerting/history";

	useEffect(() => {
		if (hasAlertingAccess && isIndex) {
			void navigate({ to: defaultRoute, replace: true });
		}
	}, [defaultRoute, hasAlertingAccess, isIndex, navigate]);

	if (!hasAlertingAccess) {
		return <NoPermissionView entity="alerting" />;
	}
	if (isIndex) return null;
	return <Outlet />;
}

export const Route = createFileRoute("/workspace/alerting")({
	component: RouteComponent,
});