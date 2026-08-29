import PageTitle from "@/components/pageTitle";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { getErrorMessage } from "@/lib/store";
import { useListFeatureFlagsQuery, useUpdateFeatureFlagMutation } from "@/lib/store/apis/featureFlagsApi";
import type { FeatureFlagStatus } from "@/lib/types/featureFlag";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { Crown, Lock } from "lucide-react";
import { toast } from "sonner";
import i18n from "@/lib/i18n";

export default function FeatureFlagsView() {
	const hasUpdateAccess = useRbac(RbacResource.FeatureFlags, RbacOperation.Update);
	const { data, isLoading, isError, error } = useListFeatureFlagsQuery();
	const [updateFeatureFlag] = useUpdateFeatureFlagMutation();

	const flags = data?.flags ?? [];

	async function handleToggle(flag: FeatureFlagStatus, checked: boolean) {
		try {
			await updateFeatureFlag({ id: flag.id, enabled: checked }).unwrap();
			toast.success(
				i18n.t("workspace.config.featureFlagsCopy.stateChanged", {
					name: flag.display_name || flag.id,
					state: checked ? i18n.t("workspace.providers.keyTable.itemEnabled") : i18n.t("workspace.providers.keyTable.itemDisabled"),
				}),
			);
		} catch (err) {
			toast.error(getErrorMessage(err));
		}
	}

	return (
		<div className="w-full space-y-4">
			<PageTitle title={i18n.t("sidebar.sub.featureFlags")}>
				{i18n.t("workspace.config.featureFlagsCopy.featureFlagsView_toggle_in_process_feature_flags_flags_are_declared_in_co")}
			</PageTitle>

			{isLoading && (
				<p className="text-muted-foreground text-sm">
					{i18n.t("workspace.config.featureFlagsCopy.featureFlagsView_loading_feature_flags")}
				</p>
			)}
			{isError && (
				<p className="text-sm text-red-500">
					{i18n.t("workspace.config.featureFlagsCopy.featureFlagsView_failed_to_load_feature_flags")}: {getErrorMessage(error)}
				</p>
			)}

			{!isLoading && !isError && (
				<div className="overflow-auto rounded-sm border">
					<Table data-testid="feature-flags-table">
						<TableHeader>
							<TableRow className="bg-muted/50">
								<TableHead className="font-semibold">{i18n.t("workspace.config.featureFlagsCopy.featureFlagsView_flag")}</TableHead>
								<TableHead className="w-px text-right font-semibold">
									{i18n.t("workspace.config.featureFlagsCopy.featureFlagsView_enabled")}
								</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{flags.length === 0 ? (
								<TableRow data-testid="feature-flags-table-empty-state">
									<TableCell colSpan={2} className="h-24 text-center">
										<span className="text-muted-foreground text-sm">
											{i18n.t("workspace.config.featureFlagsCopy.featureFlagsView_no_feature_flags_found")}
										</span>
									</TableCell>
								</TableRow>
							) : (
								flags.map((flag) => <FeatureFlagRow key={flag.id} flag={flag} canUpdate={hasUpdateAccess} onToggle={handleToggle} />)
							)}
						</TableBody>
					</Table>
				</div>
			)}
		</div>
	);
}

interface FeatureFlagRowProps {
	flag: FeatureFlagStatus;
	canUpdate: boolean;
	onToggle: (flag: FeatureFlagStatus, checked: boolean) => Promise<void>;
}

function FeatureFlagRow({ flag, canUpdate, onToggle }: FeatureFlagRowProps) {
	const disabled = flag.locked || !flag.registered || !canUpdate;
	// Fall back to id when display_name is empty so unregistered orphans
	// still render something readable in the primary slot.
	const primaryLabel = flag.display_name || flag.id;

	return (
		<TableRow className="group hover:bg-muted/50 transition-colors">
			<TableCell className="align-top">
				<div className="flex flex-col gap-1">
					<div className="flex flex-wrap items-center gap-2">
						<span className="text-sm font-medium">{primaryLabel}</span>
						{flag.display_name && <span className="text-muted-foreground font-mono text-xs">{flag.id}</span>}
						<SourceBadge source={flag.source} />
						{flag.enterprise_only && <EnterpriseBadge />}
						{flag.locked && !flag.enterprise_only && <LockedBadge />}
						{!flag.registered && <UnregisteredBadge />}
					</div>
					{flag.description && <p className="text-muted-foreground text-sm">{flag.description}</p>}
					{!flag.registered && (
						<p className="text-muted-foreground text-xs">
							{i18n.t("workspace.config.featureFlagsCopy.featureFlagsView_no_code_currently_reads_this_flag_the_override_is_stored")}
						</p>
					)}
				</div>
			</TableCell>
			<TableCell className="w-px text-right align-top">
				<Switch
					data-testid={`feature-flag-toggle-${flag.id}`}
					size="md"
					checked={flag.enabled}
					disabled={disabled}
					onAsyncCheckedChange={(checked) => onToggle(flag, checked)}
				/>
			</TableCell>
		</TableRow>
	);
}

function SourceBadge({ source }: { source: FeatureFlagStatus["source"] }) {
	return (
		<Badge variant="outline" className="text-xs capitalize">
			{source}
		</Badge>
	);
}

function LockedBadge() {
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<Badge variant="secondary" className="flex items-center gap-1 text-xs">
					<Lock className="size-3" />
					{i18n.t("workspace.config.featureFlagsCopy.featureFlagsView_locked")}
				</Badge>
			</TooltipTrigger>
			<TooltipContent>
				{i18n.t("workspace.config.featureFlagsCopy.featureFlagsView_value_is_pinned_by_config_json_or_helm_edit_your_config_")}
			</TooltipContent>
		</Tooltip>
	);
}

function EnterpriseBadge() {
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<Badge variant="secondary" className="flex items-center gap-1 text-xs">
					<Crown className="size-3" />
					{i18n.t("workspace.config.proxy.enterprise")}
				</Badge>
			</TooltipTrigger>
			<TooltipContent>
				{i18n.t("workspace.config.featureFlagsCopy.featureFlagsView_this_flag_gates_an_enterprise_only_feature_upgrade_to_en")}
			</TooltipContent>
		</Tooltip>
	);
}

function UnregisteredBadge() {
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<Badge variant="destructive" className="text-xs">
					{i18n.t("workspace.config.featureFlagsCopy.featureFlagsView_unregistered")}
				</Badge>
			</TooltipTrigger>
			<TooltipContent>
				{i18n.t("workspace.config.featureFlagsCopy.featureFlagsView_this_id_has_no_code_registration_restore_the_register_ca")}
			</TooltipContent>
		</Tooltip>
	);
}