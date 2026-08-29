import { getErrorMessage, useAppSelector, useUpdatePluginMutation } from "@/lib/store";
import { MaximConfigSchema, MaximFormSchema } from "@/lib/types/schemas";
import { useMemo } from "react";
import { toast } from "sonner";
import { MaximFormFragment } from "../../fragments/maximFormFragment";
import i18n from "@/lib/i18n";
import { Trans } from "react-i18next";

interface MaximViewProps {
	onDelete?: () => void;
	isDeleting?: boolean;
}

export default function MaximView({ onDelete, isDeleting }: MaximViewProps) {
	const selectedPlugin = useAppSelector((state) => state.plugin.selectedPlugin);
	const [updatePlugin] = useUpdatePluginMutation();
	const currentConfig = useMemo(
		() => ({ ...((selectedPlugin?.config as MaximConfigSchema) ?? {}), enabled: selectedPlugin?.enabled }),
		[selectedPlugin],
	);

	const handleMaximConfigSave = (config: MaximFormSchema): Promise<void> => {
		return new Promise((resolve, reject) => {
			updatePlugin({
				name: "maxim",
				data: {
					enabled: config.enabled,
					config: config.maxim_config,
				},
			})
				.unwrap()
				.then(() => {
					toast.success(i18n.t("workspace.observability.maximForm.configurationUpdated"));
					resolve();
				})
				.catch((err) => {
					toast.error(i18n.t("workspace.observability.maximForm.configurationUpdateFailed"), {
						description: getErrorMessage(err),
					});
					reject(err);
				});
		});
	};

	return (
		<div className="flex w-full flex-col gap-4">
			<div className="flex w-full flex-col gap-2">
				<div className="text-muted-foreground text-xs font-medium">{i18n.t("workspace.observability.maximForm.configuration")}</div>
				<div className="text-muted-foreground mb-2 text-xs font-normal">
					<Trans i18nKey="workspace.observability.maximForm.configurationDescription" components={{ code: <code /> }} />
				</div>
				<MaximFormFragment onSave={handleMaximConfigSave} initialConfig={currentConfig} onDelete={onDelete} isDeleting={isDeleting} />
			</div>
		</div>
	);
}