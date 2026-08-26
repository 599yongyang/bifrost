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
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { RenderProviderIcon, type ProviderIconType } from "@/lib/constants/icons";
import { getProviderLabel } from "@/lib/constants/logs";
import i18n from "@/lib/i18n";
import { getErrorMessage, useCreatePluginMutation, useGetPluginsQuery, useUpdatePluginMutation } from "@/lib/store";
import { CIRCUIT_BREAKER_PLUGIN, type CircuitBreakerConfig, type CircuitBreakerPolicy } from "@/lib/types/circuitBreaker";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { ArrowRight, BookOpen, CircuitBoard, Edit3, MoreHorizontal, Plus, Search, ShieldCheck, Trash2 } from "lucide-react";
import { useMemo, useState } from "react";
import { toast } from "sonner";
import { CircuitBreakerSheet } from "./circuitBreakerSheet";

function Endpoint({ provider, model }: { provider: string; model: string }) {
	return (
		<div className="flex min-w-0 items-center gap-2">
			<div className="bg-muted flex size-7 shrink-0 items-center justify-center rounded-md border">
				<RenderProviderIcon provider={provider as ProviderIconType} size="sm" className="size-4" />
			</div>
			<div className="min-w-0">
				<div className="text-foreground truncate text-xs font-medium">{getProviderLabel(provider)}</div>
				<div className="text-muted-foreground truncate font-mono text-[11px]">{model}</div>
			</div>
		</div>
	);
}

function TrafficPath({ policy }: { policy: CircuitBreakerPolicy }) {
	return (
		<div className="grid min-w-[440px] grid-cols-[minmax(130px,1fr)_64px_minmax(130px,1fr)] items-center gap-2">
			<Endpoint provider={policy.primary_provider} model={policy.primary_model} />
			<div className="text-muted-foreground flex items-center justify-center" aria-hidden="true">
				<div className="bg-border h-px flex-1" />
				<div className="border-border bg-background mx-1 flex size-7 items-center justify-center rounded-full border shadow-xs">
					<CircuitBoard className="size-3.5 text-amber-600 dark:text-amber-400" />
				</div>
				<ArrowRight className="size-3.5" />
			</div>
			<Endpoint provider={policy.fallback_provider} model={policy.fallback_model} />
		</div>
	);
}

function PolicySignals({ policy }: { policy: CircuitBreakerPolicy }) {
	const signals = policy.condition.signals;
	return (
		<div className="flex max-w-[260px] flex-wrap items-center gap-1">
			<Badge variant="outline" className="h-5 rounded-sm px-1.5 font-mono text-[10px]">
				{policy.condition.operator ?? "OR"}
			</Badge>
			{signals.slice(0, 2).map((signal, index) => (
				<Badge
					key={`${signal.header_name}-${index}`}
					variant="secondary"
					className="h-5 max-w-[180px] rounded-sm px-1.5 font-mono text-[10px]"
				>
					<span className="truncate">{signal.header_name}</span>
				</Badge>
			))}
			{signals.length > 2 && <span className="text-muted-foreground text-xs">+{signals.length - 2}</span>}
		</div>
	);
}

export function CircuitBreakerView() {
	const { data: plugins, isLoading } = useGetPluginsQuery();
	const plugin = useMemo(() => plugins?.find((item) => item.name === CIRCUIT_BREAKER_PLUGIN), [plugins]);
	const policies = useMemo(() => (plugin?.config as CircuitBreakerConfig | undefined)?.policies ?? [], [plugin]);
	const [search, setSearch] = useState("");
	const [sheetOpen, setSheetOpen] = useState(false);
	const [editingPolicy, setEditingPolicy] = useState<CircuitBreakerPolicy | null>(null);
	const [deletePolicy, setDeletePolicy] = useState<CircuitBreakerPolicy | null>(null);
	const [createPlugin, { isLoading: isCreating }] = useCreatePluginMutation();
	const [updatePlugin, { isLoading: isUpdating }] = useUpdatePluginMutation();
	const canCreate = useRbac(RbacResource.CircuitBreaker, RbacOperation.Create);
	const canUpdate = useRbac(RbacResource.CircuitBreaker, RbacOperation.Update);
	const canDelete = useRbac(RbacResource.CircuitBreaker, RbacOperation.Delete);
	const isSaving = isCreating || isUpdating;

	const visiblePolicies = useMemo(() => {
		const query = search.trim().toLowerCase();
		if (!query) return policies;
		return policies.filter((policy) =>
			[
				policy.name,
				policy.primary_provider,
				policy.primary_model,
				policy.fallback_provider,
				policy.fallback_model,
				...policy.condition.signals.map((signal) => signal.header_name),
			]
				.join(" ")
				.toLowerCase()
				.includes(query),
		);
	}, [policies, search]);

	const persistPolicies = async (nextPolicies: CircuitBreakerPolicy[]) => {
		const config: CircuitBreakerConfig = { policies: nextPolicies };
		if (plugin) {
			await updatePlugin({
				name: CIRCUIT_BREAKER_PLUGIN,
				data: { enabled: nextPolicies.length > 0, config },
			}).unwrap();
		} else {
			await createPlugin({
				name: CIRCUIT_BREAKER_PLUGIN,
				path: "",
				enabled: nextPolicies.length > 0,
				config,
			}).unwrap();
		}
	};

	const handleSave = async (policy: CircuitBreakerPolicy, originalName?: string) => {
		const nextPolicies = originalName ? policies.map((item) => (item.name === originalName ? policy : item)) : [...policies, policy];
		await persistPolicies(nextPolicies);
		toast.success(originalName ? i18n.t("workspace.circuitBreaker.updated") : i18n.t("workspace.circuitBreaker.created"));
	};

	const handleToggle = async (policy: CircuitBreakerPolicy, enabled: boolean) => {
		try {
			await persistPolicies(policies.map((item) => (item.name === policy.name ? { ...item, enabled } : item)));
			toast.success(enabled ? i18n.t("workspace.circuitBreaker.enabled") : i18n.t("workspace.circuitBreaker.disabled"));
		} catch (error) {
			toast.error(getErrorMessage(error));
			throw error;
		}
	};

	const handleDelete = async () => {
		if (!deletePolicy) return;
		try {
			await persistPolicies(policies.filter((item) => item.name !== deletePolicy.name));
			toast.success(i18n.t("workspace.circuitBreaker.deleted"));
			setDeletePolicy(null);
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	};

	const openCreate = () => {
		setEditingPolicy(null);
		setSheetOpen(true);
	};

	if (!isLoading && policies.length === 0) {
		return (
			<>
				<div className="flex min-h-[70vh] flex-col items-center justify-center px-6 text-center">
					<div className="relative mb-7 flex w-full max-w-sm items-center justify-center" aria-hidden="true">
						<div className="bg-border h-px flex-1" />
						<div className="bg-background border-border mx-3 flex size-16 items-center justify-center rounded-2xl border shadow-sm">
							<CircuitBoard className="size-8 text-amber-600 dark:text-amber-400" strokeWidth={1.5} />
						</div>
						<div className="bg-border h-px flex-1" />
					</div>
					<h1 className="text-foreground text-xl font-semibold tracking-tight">{i18n.t("workspace.circuitBreaker.emptyTitle")}</h1>
					<p className="text-muted-foreground mt-2 max-w-lg text-sm leading-6">{i18n.t("workspace.circuitBreaker.emptyDescription")}</p>
					<div className="mt-6 flex items-center gap-2">
						<Button variant="outline" size="sm" asChild>
							<a
								href="https://docs.getbifrost.ai/enterprise/circuit-breaker"
								target="_blank"
								rel="noreferrer"
								data-testid="circuit-breaker-docs-link"
							>
								<BookOpen className="size-4" />
								{i18n.t("workspace.circuitBreaker.readDocs")}
							</a>
						</Button>
						{canCreate && (
							<Button size="sm" onClick={openCreate} data-testid="create-circuit-breaker-policy-btn">
								<Plus className="size-4" />
								{i18n.t("workspace.circuitBreaker.newPolicy")}
							</Button>
						)}
					</div>
				</div>
				<CircuitBreakerSheet open={sheetOpen} onOpenChange={setSheetOpen} policies={policies} onSave={handleSave} />
			</>
		);
	}

	return (
		<div className="flex min-h-0 flex-1 flex-col overflow-y-auto">
			<div className="mb-4 flex flex-wrap items-start justify-between gap-3">
				<div>
					<div className="flex items-center gap-2">
						<h1 className="text-foreground text-lg font-semibold">{i18n.t("workspace.circuitBreaker.title")}</h1>
						<Badge variant="secondary" className="rounded-sm font-mono text-[10px]">
							{policies.filter((policy) => policy.enabled !== false).length}/{policies.length} {i18n.t("workspace.circuitBreaker.active")}
						</Badge>
					</div>
					<p className="text-muted-foreground text-sm">{i18n.t("workspace.circuitBreaker.description")}</p>
				</div>
				{canCreate && (
					<Button size="sm" onClick={openCreate} data-testid="create-circuit-breaker-policy-btn">
						<Plus className="size-4" />
						{i18n.t("workspace.circuitBreaker.newPolicy")}
					</Button>
				)}
			</div>

			<div className="mb-4 max-w-sm">
				<div className="relative">
					<Search className="text-muted-foreground absolute top-1/2 left-3 size-4 -translate-y-1/2" />
					<Input
						value={search}
						onChange={(event) => setSearch(event.target.value)}
						placeholder={i18n.t("workspace.circuitBreaker.search")}
						className="pl-9"
						data-testid="circuit-breaker-search-input"
					/>
				</div>
			</div>

			<div className="overflow-hidden rounded-sm border">
				<Table containerClassName="overflow-auto">
					<TableHeader className="bg-muted sticky top-0 z-10">
						<TableRow className="bg-muted/50">
							<TableHead className="w-[180px] font-semibold">{i18n.t("workspace.circuitBreaker.policy")}</TableHead>
							<TableHead className="min-w-[470px] font-semibold">{i18n.t("workspace.circuitBreaker.trafficPath")}</TableHead>
							<TableHead className="font-semibold">{i18n.t("workspace.circuitBreaker.signal")}</TableHead>
							<TableHead className="font-semibold">{i18n.t("workspace.circuitBreaker.cooldown")}</TableHead>
							<TableHead className="w-[90px] font-semibold">{i18n.t("workspace.circuitBreaker.status")}</TableHead>
							<TableHead className="w-[52px]" />
						</TableRow>
					</TableHeader>
					<TableBody>
						{isLoading ? (
							[0, 1, 2].map((row) => (
								<TableRow key={row}>
									<TableCell colSpan={6}>
										<Skeleton className="h-8 w-full" />
									</TableCell>
								</TableRow>
							))
						) : visiblePolicies.length === 0 ? (
							<TableRow>
								<TableCell colSpan={6} className="text-muted-foreground h-28 text-center text-sm">
									{i18n.t("workspace.circuitBreaker.noMatching")}
								</TableCell>
							</TableRow>
						) : (
							visiblePolicies.map((policy) => (
								<TableRow key={policy.name} className="group hover:bg-muted/40">
									<TableCell>
										<div className="flex items-center gap-2">
											<ShieldCheck className="text-muted-foreground size-4 shrink-0" />
											<span className="max-w-[150px] truncate font-medium" title={policy.name}>
												{policy.name}
											</span>
										</div>
										{(policy.primary_key_ids?.length ?? 0) > 0 && (
											<div className="text-muted-foreground mt-1 pl-6 text-[11px]">
												{i18n.t("workspace.circuitBreaker.keyCircuits", { count: policy.primary_key_ids?.length })}
											</div>
										)}
									</TableCell>
									<TableCell>
										<TrafficPath policy={policy} />
									</TableCell>
									<TableCell>
										<PolicySignals policy={policy} />
									</TableCell>
									<TableCell>
										<div className="font-mono text-xs">{policy.default_cooldown || "30s"}</div>
										{policy.cooldown_header && (
											<div className="text-muted-foreground mt-0.5 max-w-[150px] truncate font-mono text-[10px]">
												↳ {policy.cooldown_header}
											</div>
										)}
									</TableCell>
									<TableCell onClick={(event) => event.stopPropagation()}>
										<Switch
											checked={policy.enabled !== false}
											disabled={!canUpdate || isSaving}
											onAsyncCheckedChange={(checked) => handleToggle(policy, checked)}
											data-testid={`circuit-breaker-policy-${policy.name}-enabled-switch`}
										/>
									</TableCell>
									<TableCell>
										<DropdownMenu>
											<DropdownMenuTrigger asChild>
												<Button
													variant="ghost"
													size="icon"
													className="size-8"
													aria-label={`${i18n.t("workspace.circuitBreaker.actions")} ${policy.name}`}
													data-testid={`circuit-breaker-policy-${policy.name}-actions-btn`}
												>
													<MoreHorizontal className="size-4" />
												</Button>
											</DropdownMenuTrigger>
											<DropdownMenuContent align="end">
												<DropdownMenuItem
													disabled={!canUpdate}
													data-testid={`circuit-breaker-policy-${policy.name}-edit-btn`}
													onSelect={() => {
														setEditingPolicy(policy);
														setSheetOpen(true);
													}}
												>
													<Edit3 className="size-4" />
													{i18n.t("common.edit")}
												</DropdownMenuItem>
												<DropdownMenuItem
													variant="destructive"
													disabled={!canDelete}
													data-testid={`circuit-breaker-policy-${policy.name}-delete-btn`}
													onSelect={() => setDeletePolicy(policy)}
												>
													<Trash2 className="size-4" />
													{i18n.t("common.delete")}
												</DropdownMenuItem>
											</DropdownMenuContent>
										</DropdownMenu>
									</TableCell>
								</TableRow>
							))
						)}
					</TableBody>
				</Table>
			</div>

			<CircuitBreakerSheet
				open={sheetOpen}
				onOpenChange={(open) => {
					setSheetOpen(open);
					if (!open) setEditingPolicy(null);
				}}
				editingPolicy={editingPolicy}
				policies={policies}
				onSave={handleSave}
			/>

			<AlertDialog open={deletePolicy !== null} onOpenChange={(open) => !open && setDeletePolicy(null)}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>{i18n.t("workspace.circuitBreaker.deleteTitle")}</AlertDialogTitle>
						<AlertDialogDescription>
							{i18n.t("workspace.circuitBreaker.deleteDescription", { name: deletePolicy?.name })}
						</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel data-testid="cancel-delete-circuit-breaker-policy-btn">{i18n.t("common.cancel")}</AlertDialogCancel>
						<AlertDialogAction data-testid="confirm-delete-circuit-breaker-policy-btn" disabled={isSaving} onClick={handleDelete}>
							{i18n.t("common.delete")}
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>
		</div>
	);
}