import { createFileRoute } from "@tanstack/react-router";
import { NoPermissionView } from "@/components/noPermissionView";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import AlertChannelsPage from "./page";

function AlertChannelsRoute() {
	const hasAccess = useRbac(RbacResource.AlertChannels, RbacOperation.View);
	return hasAccess ? <AlertChannelsPage /> : <NoPermissionView entity="alert channels" />;
}

export const Route = createFileRoute("/workspace/alerting/channels")({
	component: AlertChannelsRoute,
});