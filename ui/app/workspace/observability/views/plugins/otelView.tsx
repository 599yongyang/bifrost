import { Button } from "@/components/ui/button";
import { getErrorMessage, useAppSelector, useUpdatePluginMutation } from "@/lib/store";
import { OtelFormSchema } from "@/lib/types/schemas";
import { Activity } from "lucide-react";
import { useMemo, useState } from "react";
import { toast } from "sonner";
import { OtelFormFragment } from "../../fragments/otelFormFragment";
import { buildOtelPluginConfig } from "../../otelConfig";
import PluginTracingSheet from "../../sheets/pluginTracingSheet";

interface OtelViewProps {
	onDelete?: () => void;
	isDeleting?: boolean;
}

export default function OtelView({ onDelete, isDeleting }: OtelViewProps) {
	const selectedPlugin = useAppSelector((state) => state.plugin.selectedPlugin);
	const currentConfig = useMemo(() => ({ config: selectedPlugin?.config, enabled: selectedPlugin?.enabled }), [selectedPlugin]);
	const [updatePlugin] = useUpdatePluginMutation();
	const [isTracingSheetOpen, setIsTracingSheetOpen] = useState(false);

	const handleOtelConfigSave = (config: OtelFormSchema): Promise<void> => {
		return new Promise((resolve, reject) => {
			updatePlugin({
				name: "otel",
				data: {
					enabled: config.enabled,
					config: buildOtelPluginConfig(config),
				},
			})
				.unwrap()
				.then(() => {
					resolve();
					toast.success("OTEL configuration updated successfully");
				})
				.catch((err) => {
					toast.error("Failed to update OTEL configuration", {
						description: getErrorMessage(err),
					});
					reject(err);
				});
		});
	};

	return (
		<div className="flex w-full flex-col gap-4">
			<div className="flex w-full flex-col gap-3">
				<div className="flex justify-end">
					<Button
						type="button"
						variant="outline"
						size="sm"
						onClick={() => setIsTracingSheetOpen(true)}
						data-testid="otel-configure-tracing-button"
					>
						<Activity className="h-4 w-4" />
						Configure Plugin Tracing
					</Button>
				</div>
				<OtelFormFragment onSave={handleOtelConfigSave} currentConfig={currentConfig} onDelete={onDelete} isDeleting={isDeleting} />
			</div>
			<PluginTracingSheet
				open={isTracingSheetOpen}
				onClose={() => setIsTracingSheetOpen(false)}
				pluginName="otel"
				destination="the OTEL collector"
			/>
		</div>
	);
}
