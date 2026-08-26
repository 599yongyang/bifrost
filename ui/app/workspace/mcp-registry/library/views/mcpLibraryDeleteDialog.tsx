import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogFooter,
	AlertDialogHeader,
	AlertDialogTitle,
} from "@/components/ui/alertDialog";
import type { MCPLibraryEntry } from "@/lib/types/mcp";
import i18n from "@/lib/i18n";

interface MCPLibraryDeleteDialogProps {
	/** The entry being removed; when null the dialog is closed. */
	server: MCPLibraryEntry | null;
	open: boolean;
	isDeleting: boolean;
	onOpenChange: (open: boolean) => void;
	onConfirm: () => void;
	confirmTestId: string;
}

// Shared confirmation dialog for soft-deleting a library entry, used by both the
// card and table views. Copy is sync-aware: custom entries simply disappear,
// while remote entries are tombstoned so they don't reappear on the next sync.
export function MCPLibraryDeleteDialog({ server, open, isDeleting, onOpenChange, onConfirm, confirmTestId }: MCPLibraryDeleteDialogProps) {
	const isCustom = server?.source === "custom";

	return (
		<AlertDialog open={open} onOpenChange={onOpenChange}>
			<AlertDialogContent>
				<AlertDialogHeader>
					<AlertDialogTitle>{i18n.t("workspace.mcpLibrary.removeTitle", { name: server?.name })}</AlertDialogTitle>
					<AlertDialogDescription>
						{isCustom ? i18n.t("workspace.mcpLibrary.removeCustomDescription") : i18n.t("workspace.mcpLibrary.hideSyncedDescription")}{" "}
						{i18n.t("workspace.mcpLibrary.installationsUnaffected")}
					</AlertDialogDescription>
				</AlertDialogHeader>
				<AlertDialogFooter>
					<AlertDialogCancel disabled={isDeleting}>{i18n.t("common.cancel")}</AlertDialogCancel>
					<AlertDialogAction
						onClick={(event) => {
							event.preventDefault();
							onConfirm();
						}}
						disabled={isDeleting}
						data-testid={confirmTestId}
					>
						{isDeleting ? i18n.t("workspace.mcpLibrary.removing") : i18n.t("workspace.mcpLibrary.remove")}
					</AlertDialogAction>
				</AlertDialogFooter>
			</AlertDialogContent>
		</AlertDialog>
	);
}