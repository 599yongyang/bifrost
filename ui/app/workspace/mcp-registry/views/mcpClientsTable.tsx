import ClientForm from "@/app/workspace/mcp-registry/views/mcpClientForm";
import { PIN_SHADOW_RIGHT } from "@/components/table/columnPinning";
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
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdownMenu";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useToast } from "@/hooks/use-toast";
import { MCP_STATUS_COLORS } from "@/lib/constants/config";
import { getErrorMessage, useDeleteMCPClientMutation, useReconnectMCPClientMutation, useUpdateMCPClientMutation } from "@/lib/store";
import { MCPClient } from "@/lib/types/mcp";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { Link } from "@tanstack/react-router";
import { Box, ChevronLeft, ChevronRight, Loader2, MoreHorizontal, PencilIcon, Plus, RefreshCcw, Search, Trash2, X } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import MCPClientSheet from "./mcpClientSheet";
import { MCPServersEmptyState } from "./mcpServersEmptyState";
import { MCPUsageGuideSheet } from "./mcpUsageGuide";
import i18n from "@/lib/i18n";

function MCPClientActionsMenu({
	client,
	hasUpdateAccess,
	hasDeleteAccess,
	isReconnecting,
	isPerUserAuth,
	onEdit,
	onReconnect,
	onDelete,
}: {
	client: MCPClient;
	hasUpdateAccess: boolean;
	hasDeleteAccess: boolean;
	isReconnecting: boolean;
	isPerUserAuth: boolean;
	onEdit: (client: MCPClient) => void;
	onReconnect: (client: MCPClient) => void;
	onDelete: (client: MCPClient) => void;
}) {
	const [isOpen, setIsOpen] = useState(false);

	return (
		<DropdownMenu open={isOpen} onOpenChange={setIsOpen}>
			<DropdownMenuTrigger asChild>
				<Button
					variant="ghost"
					size="icon"
					className="h-8 w-8"
					aria-label={i18n.t("supplemental.mcpServerActions")}
					data-testid={`mcp-client-actions-${client.config.client_id}-btn`}
				>
					{isReconnecting ? <Loader2 className="h-4 w-4 animate-spin" /> : <MoreHorizontal className="h-4 w-4" />}
				</Button>
			</DropdownMenuTrigger>
			<DropdownMenuContent
				align="end"
				onCloseAutoFocus={(e) => {
					// Edit opens a Sheet; letting the dropdown restore focus to its
					// trigger fights the Sheet's autofocus and leaves focus outside
					// the dialog — which breaks ESC-to-close. Hand focus off to the
					// Sheet by skipping the dropdown's auto-restore.
					e.preventDefault();
				}}
			>
				{hasUpdateAccess && (
					<DropdownMenuItem
						className="cursor-pointer"
						data-testid={`mcp-client-edit-${client.config.client_id}-menu-item`}
						onSelect={(e) => {
							e.preventDefault();
							onEdit(client);
							setIsOpen(false);
						}}
					>
						<PencilIcon className="h-4 w-4" />
						{i18n.t("common.edit")}
					</DropdownMenuItem>
				)}
				{hasUpdateAccess && (
					<DropdownMenuItem
						className="cursor-pointer"
						disabled={isPerUserAuth || client.config.disabled || isReconnecting}
						onSelect={(e) => {
							e.preventDefault();
							onReconnect(client);
							setIsOpen(false);
						}}
					>
						<RefreshCcw className="h-4 w-4" />
						{i18n.t("workspace.mcp.reconnect")}
					</DropdownMenuItem>
				)}
				{hasDeleteAccess && (
					<DropdownMenuItem
						variant="destructive"
						className="cursor-pointer"
						onSelect={(e) => {
							e.preventDefault();
							onDelete(client);
							setIsOpen(false);
						}}
					>
						<Trash2 className="h-4 w-4" />
						{i18n.t("common.delete")}
					</DropdownMenuItem>
				)}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}

interface MCPClientsTableProps {
	mcpClients: MCPClient[];
	totalCount: number;
	refetch?: () => void;
	search: string;
	debouncedSearch: string;
	server: string;
	/** Whether any sidebar facet filter (connection/auth/code-mode/status) is active. */
	filtersActive?: boolean;
	onSearchChange: (value: string) => void;
	onServerFilterClear: () => void;
	offset: number;
	limit: number;
	onOffsetChange: (offset: number) => void;
}

export default function MCPClientsTable({
	mcpClients,
	totalCount,
	refetch,
	search,
	debouncedSearch,
	server,
	filtersActive = false,
	onSearchChange,
	onServerFilterClear,
	offset,
	limit,
	onOffsetChange,
}: MCPClientsTableProps) {
	const [formOpen, setFormOpen] = useState(false);
	const hasCreateMCPClientAccess = useRbac(RbacResource.MCPGateway, RbacOperation.Create);
	const hasUpdateMCPClientAccess = useRbac(RbacResource.MCPGateway, RbacOperation.Update);
	const hasDeleteMCPClientAccess = useRbac(RbacResource.MCPGateway, RbacOperation.Delete);
	const [selectedMCPClient, setSelectedMCPClient] = useState<MCPClient | null>(null);
	const [clientToDelete, setClientToDelete] = useState<MCPClient | null>(null);
	const [showDetailSheet, setShowDetailSheet] = useState(false);
	const { toast } = useToast();

	const [reconnectingClients, setReconnectingClients] = useState<string[]>([]);
	const [togglingClientIds, setTogglingClientIds] = useState<Set<string>>(new Set());

	// RTK Query mutations
	const [reconnectMCPClient] = useReconnectMCPClientMutation();
	const [deleteMCPClient] = useDeleteMCPClientMutation();
	const [updateMCPClient] = useUpdateMCPClientMutation();

	const handleCreate = () => {
		setFormOpen(true);
	};

	const handleReconnect = async (client: MCPClient) => {
		try {
			setReconnectingClients((prev) => [...prev, client.config.client_id]);
			await reconnectMCPClient(client.config.client_id).unwrap();
			setReconnectingClients((prev) => prev.filter((id) => id !== client.config.client_id));
			toast({
				title: i18n.t("workspace.mcp.reconnectedTitle"),
				description: i18n.t("workspace.mcp.clientReconnected", { name: client.config.name }),
			});
			if (refetch) {
				await refetch();
			}
		} catch (error) {
			setReconnectingClients((prev) => prev.filter((id) => id !== client.config.client_id));
			toast({ title: i18n.t("workspace.mcp.errorTitle"), description: getErrorMessage(error), variant: "destructive" });
		}
	};

	const handleDelete = async (client: MCPClient) => {
		try {
			await deleteMCPClient(client.config.client_id).unwrap();
			toast({
				title: i18n.t("workspace.mcp.deletedTitle"),
				description: i18n.t("workspace.mcp.clientRemoved", { name: client.config.name }),
			});
			if (refetch) {
				await refetch();
			}
		} catch (error) {
			toast({ title: i18n.t("workspace.mcp.errorTitle"), description: getErrorMessage(error), variant: "destructive" });
		}
	};

	const handleSaved = async () => {
		setFormOpen(false);
		if (refetch) {
			await refetch();
		}
	};

	const getConnectionTypeDisplay = (type: string) => {
		switch (type) {
			case "http":
				return "HTTP";
			case "sse":
				return "SSE";
			case "stdio":
				return "STDIO";
			default:
				return type.toUpperCase();
		}
	};

	const getAuthTypeDisplay = (type: string | undefined) => {
		switch (type) {
			case "none":
			case undefined:
			case "":
				return "None";
			case "headers":
			case "per_user_headers":
				return "Headers";
			case "oauth":
			case "per_user_oauth":
				return "OAuth";
			default:
				return type;
		}
	};

	const getAuthScopeDisplay = (type: string | undefined) => {
		switch (type) {
			case "per_user_oauth":
			case "per_user_headers":
				return "Per-User";
			case "oauth":
			case "headers":
				return "Shared";
			default:
				return "-";
		}
	};

	const handleRowClick = (mcpClient: MCPClient) => {
		setSelectedMCPClient(mcpClient);
		setShowDetailSheet(true);
	};

	const handleDetailSheetClose = () => {
		setShowDetailSheet(false);
		setSelectedMCPClient(null);
	};

	const selectedMCPClientIndex = useMemo(
		() => (selectedMCPClient ? mcpClients.findIndex((c) => c.config.client_id === selectedMCPClient.config.client_id) : -1),
		[selectedMCPClient, mcpClients],
	);

	const [pendingEdgeNav, setPendingEdgeNav] = useState<"first" | "last" | null>(null);

	useEffect(() => {
		if (pendingEdgeNav && mcpClients.length > 0) {
			const target = pendingEdgeNav === "first" ? mcpClients[0] : mcpClients[mcpClients.length - 1];
			setSelectedMCPClient(target);
			setPendingEdgeNav(null);
		}
	}, [pendingEdgeNav, mcpClients]);

	const handleDetailNavigate = (direction: "prev" | "next") => {
		const newIndex = direction === "prev" ? selectedMCPClientIndex - 1 : selectedMCPClientIndex + 1;
		if (newIndex >= 0 && newIndex < mcpClients.length) {
			setSelectedMCPClient(mcpClients[newIndex]);
		} else if (direction === "next" && offset + limit < totalCount) {
			onOffsetChange(offset + limit);
			setPendingEdgeNav("first");
		} else if (direction === "prev" && offset > 0) {
			onOffsetChange(Math.max(0, offset - limit));
			setPendingEdgeNav("last");
		}
	};

	const handleEditTools = async () => {
		setShowDetailSheet(false);
		setSelectedMCPClient(null);
		if (refetch) {
			await refetch();
		}
	};

	const hasActiveFilters = Boolean(debouncedSearch) || Boolean(server) || filtersActive;

	// True empty state: no servers at all (not just filtered to zero)
	if (totalCount === 0 && !hasActiveFilters) {
		return (
			<>
				{formOpen && <ClientForm open={formOpen} onClose={() => setFormOpen(false)} onSaved={handleSaved} />}
				<MCPServersEmptyState onAddClick={handleCreate} canCreate={hasCreateMCPClientAccess} />
			</>
		);
	}

	return (
		<div className="flex grow flex-col overflow-auto">
			{showDetailSheet && selectedMCPClient && (
				<MCPClientSheet
					mcpClient={selectedMCPClient}
					onClose={handleDetailSheetClose}
					onSubmitSuccess={handleEditTools}
					onNavigate={handleDetailNavigate}
					hasPrev={selectedMCPClientIndex > 0 || offset > 0}
					hasNext={(selectedMCPClientIndex >= 0 && selectedMCPClientIndex < mcpClients.length - 1) || offset + limit < totalCount}
				/>
			)}
			<AlertDialog open={!!clientToDelete} onOpenChange={(open) => !open && setClientToDelete(null)}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>{i18n.t("workspace.mcp.removeServerTitle")}</AlertDialogTitle>
						<AlertDialogDescription>
							{i18n.t("workspace.mcp.removeServerDescription", { name: clientToDelete?.config.name })}
						</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel>{i18n.t("common.cancel")}</AlertDialogCancel>
						<AlertDialogAction
							onClick={() => {
								if (clientToDelete) void handleDelete(clientToDelete);
							}}
							className="bg-destructive hover:bg-destructive/90"
						>
							{i18n.t("common.delete")}
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>

			<div className="mb-4 flex items-center justify-between gap-4">
				<div>
					<h2 className="text-lg font-semibold tracking-tight">{i18n.t("supplemental.mcpServerCatalog")}</h2>
					<p className="text-muted-foreground text-sm">{i18n.t("supplemental.manageMcpServers")}</p>
				</div>
				<div className="flex gap-2">
					<MCPUsageGuideSheet />
					<Button asChild variant="outline" data-testid="mcp-library-link-btn" className="h-8">
						<Link to="/workspace/mcp-registry/library">
							<Box />
							<span className="hidden sm:inline">{i18n.t("supplemental.library")}</span>
						</Link>
					</Button>
					<Button
						onClick={handleCreate}
						disabled={!hasCreateMCPClientAccess}
						data-testid="create-mcp-client-btn"
						aria-label={i18n.t("workspace.mcpForm.newServerTitle")}
						className="h-8 gap-2"
					>
						<Plus />
						<span className="hidden sm:inline">{i18n.t("workspace.mcpForm.newServerTitle")}</span>
					</Button>
				</div>
			</div>

			{/* Toolbar: Search */}
			<div className="mb-4 flex items-center gap-3">
				<div className="relative max-w-sm flex-1">
					<Search className="text-muted-foreground absolute top-1/2 left-3 h-4 w-4 -translate-y-1/2" />
					<Input
						aria-label={i18n.t("supplemental.searchMcpServers")}
						placeholder={i18n.t("workspace.mcp.searchPlaceholder")}
						value={search}
						onChange={(e) => onSearchChange(e.target.value)}
						className="pl-9"
						data-testid="mcp-clients-search-input"
					/>
				</div>
				{server && (
					<Button
						variant="outline"
						size="sm"
						className="h-8 gap-2"
						onClick={onServerFilterClear}
						data-testid="mcp-client-server-filter-clear-btn"
					>
						{i18n.t("supplemental.serverFilter")}
						<X className="size-3" />
					</Button>
				)}
			</div>

			<div className="flex grow flex-col overflow-auto">
				<div className="mb-2 grow overflow-auto rounded-sm border">
					<Table data-testid="mcp-clients-table" className="w-full min-w-[1516px] table-fixed">
						<TableHeader className="sticky top-0">
							<TableRow className="bg-muted/50">
								<TableHead className="w-[260px] font-semibold">{i18n.t("workspace.mcp.name")}</TableHead>
								<TableHead className="w-[150px] font-semibold">{i18n.t("workspace.mcp.connectionType")}</TableHead>
								<TableHead className="w-[150px] font-semibold">{i18n.t("supplemental.authType")}</TableHead>
								<TableHead className="w-[140px] font-semibold">{i18n.t("supplemental.authScope")}</TableHead>
								<TableHead className="w-[120px] font-semibold">{i18n.t("workspace.mcp.codeMode")}</TableHead>
								<TableHead className="w-[120px] font-semibold">{i18n.t("supplemental.vkAccess")}</TableHead>
								<TableHead className="w-[130px] font-semibold">{i18n.t("workspace.mcp.enabledTools")}</TableHead>
								<TableHead className="w-[160px] font-semibold">{i18n.t("supplemental.autoExecuteTools")}</TableHead>
								<TableHead className="w-[140px] font-semibold">{i18n.t("workspace.mcp.state")}</TableHead>
								<TableHead className="w-[90px] font-semibold">{i18n.t("workspace.virtualKeys.status")}</TableHead>
								<TableHead className={`bg-muted/50 sticky right-0 z-10 w-14 text-right ${PIN_SHADOW_RIGHT}`}></TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{mcpClients.length === 0 ? (
								<TableRow>
									<TableCell colSpan={11} className="h-24 text-center">
										<span className="text-muted-foreground text-sm">{i18n.t("supplemental.noMatchingMcpServers")}</span>
									</TableCell>
								</TableRow>
							) : (
								mcpClients.map((c: MCPClient) => {
									// Per-user auth types (OAuth + headers) don't hold a shared
									// upstream connection, so reconnect is a no-op for them — the
									// backend's ReconnectClient rejects with ErrMCPReconnectNotApplicable.
									const isPerUserAuth = c.config.auth_type === "per_user_oauth" || c.config.auth_type === "per_user_headers";
									const enabledToolsCount =
										c.state == "connected"
											? c.config.tools_to_execute?.includes("*")
												? c.tools?.length
												: (c.config.tools_to_execute?.length ?? 0)
											: 0;
									const autoExecuteToolsCount =
										c.state == "connected"
											? c.config.tools_to_auto_execute?.includes("*")
												? c.tools?.length
												: (c.config.tools_to_auto_execute?.length ?? 0)
											: 0;
									return (
										<TableRow key={c.config.client_id} className="group hover:bg-muted/50 transition-colors">
											<TableCell className="font-medium">
												<div className="truncate" title={c.config.name}>
													{c.config.name}
												</div>
											</TableCell>
											<TableCell data-testid="mcp-client-connection-type">
												<Badge variant="outline" className="font-mono">
													{getConnectionTypeDisplay(c.config.connection_type)}
												</Badge>
											</TableCell>
											<TableCell data-testid="mcp-client-auth-type">{getAuthTypeDisplay(c.config.auth_type)}</TableCell>
											<TableCell data-testid="mcp-client-auth-scope">{getAuthScopeDisplay(c.config.auth_type)}</TableCell>
											<TableCell>
												<Badge
													className={
														c.state == "connected" ? MCP_STATUS_COLORS[c.config.is_code_mode_client ? "connected" : "disconnected"] : ""
													}
												>
													{c.state == "connected"
														? c.config.is_code_mode_client
															? i18n.t("workspace.mcp.enabled")
															: i18n.t("workspace.mcp.disabled")
														: "-"}
												</Badge>
											</TableCell>
											<TableCell data-testid="mcp-client-vk-access">
												{c.config.allow_on_all_virtual_keys
													? i18n.t("common.all")
													: c.vk_configs?.length
														? `${c.vk_configs.length} VK`
														: i18n.t("workspace.mcpForm.none")}
											</TableCell>
											<TableCell>
												{c.state == "connected" ? (
													<>
														{enabledToolsCount}/{c.tools?.length}
													</>
												) : (
													"-"
												)}
											</TableCell>
											<TableCell>
												{c.state == "connected" ? (
													<>
														{autoExecuteToolsCount}/{c.tools?.length}
													</>
												) : (
													"-"
												)}
											</TableCell>
											<TableCell>
												<Badge className={MCP_STATUS_COLORS[c.state]}>{c.state}</Badge>
											</TableCell>
											<TableCell onClick={(e) => e.stopPropagation()}>
												<Switch
													data-testid={`mcp-client-enabled-switch-${c.config.client_id}`}
													checked={!c.config.disabled}
													size="md"
													disabled={!hasUpdateMCPClientAccess || togglingClientIds.has(c.config.client_id)}
													onAsyncCheckedChange={async (checked) => {
														setTogglingClientIds((prev) => new Set(prev).add(c.config.client_id));
														// PUT has PATCH semantics: omitted fields keep their stored value.
														// Send only `disabled` — echoing back fields from the GET response
														// re-submits them in the response's units, which differ from the
														// units PUT expects (e.g. tool_sync_interval is ns out, minutes in).
														await updateMCPClient({
															id: c.config.client_id,
															data: {
																disabled: !checked,
															},
														})
															.unwrap()
															.then(() => {
																toast({ title: `Server ${checked ? "enabled" : "disabled"} successfully` });
																if (refetch) refetch();
															})
															.catch((err) => {
																toast({
																	title: i18n.t("workspace.mcp.errorTitle"),
																	description: getErrorMessage(err),
																	variant: "destructive",
																});
															})
															.finally(() => {
																setTogglingClientIds((prev) => {
																	const next = new Set(prev);
																	next.delete(c.config.client_id);
																	return next;
																});
															});
													}}
												/>
											</TableCell>
											<TableCell
												className={`bg-card group-hover:bg-muted/50 sticky right-0 z-10 text-right ${PIN_SHADOW_RIGHT}`}
												onClick={(e) => e.stopPropagation()}
											>
												<MCPClientActionsMenu
													client={c}
													hasUpdateAccess={hasUpdateMCPClientAccess}
													hasDeleteAccess={hasDeleteMCPClientAccess}
													isReconnecting={reconnectingClients.includes(c.config.client_id)}
													isPerUserAuth={isPerUserAuth}
													onEdit={handleRowClick}
													onReconnect={(client) => void handleReconnect(client)}
													onDelete={setClientToDelete}
												/>
											</TableCell>
										</TableRow>
									);
								})
							)}
						</TableBody>
					</Table>
				</div>

				{/* Pagination */}
				{totalCount > 0 && (
					<div className="flex shrink-0 items-center justify-between text-xs" data-testid="pagination">
						<div className="text-muted-foreground flex items-center gap-2">
							{i18n.t("workspace.mcp.showing", {
								from: (offset + 1).toLocaleString(),
								to: Math.min(offset + limit, totalCount).toLocaleString(),
								total: totalCount.toLocaleString(),
							})}
						</div>

						<div className="flex items-center gap-2">
							<Button
								variant="ghost"
								size="sm"
								onClick={() => onOffsetChange(Math.max(0, offset - limit))}
								disabled={offset === 0}
								data-testid="mcp-clients-pagination-prev-btn"
								aria-label={i18n.t("supplemental.previousPage")}
							>
								<ChevronLeft className="size-3" />
							</Button>

							<div className="flex items-center gap-1">
								<span>{i18n.t("supplemental.page")}</span>
								<span>{Math.floor(offset / limit) + 1}</span>
								<span>
									{i18n.t("supplemental.of")} {Math.ceil(totalCount / limit)}
								</span>
							</div>

							<Button
								variant="ghost"
								size="sm"
								onClick={() => onOffsetChange(offset + limit)}
								disabled={offset + limit >= totalCount}
								data-testid="mcp-clients-pagination-next-btn"
								aria-label={i18n.t("supplemental.nextPage")}
							>
								<ChevronRight className="size-3" />
							</Button>
						</div>
					</div>
				)}
			</div>

			{formOpen && <ClientForm open={formOpen} onClose={() => setFormOpen(false)} onSaved={handleSaved} />}
		</div>
	);
}