import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Form, FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from "@/components/ui/form";
import { HeadersTable } from "@/components/ui/headersTable";
import { Input } from "@/components/ui/input";
import { RequestHeadersTextarea } from "@/components/ui/requestHeadersTextarea";
import { SecretVarInput } from "@/components/ui/secretVarInput";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { otelFormSchema, type OtelFormSchema, type SecretVar } from "@/lib/types/schemas";
import { emptySecretVar, toSecretVarFormValue, toSecretVarMapFormValue } from "@/lib/utils/secretVarForm";
import { cn } from "@/lib/utils";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { DragDropProvider } from "@dnd-kit/react";
import { useSortable } from "@dnd-kit/react/sortable";
import { zodResolver } from "@hookform/resolvers/zod";
import { CheckCircle2, ChevronDown, GripVertical, Info, Plus, Settings2, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { useFieldArray, useForm, type Control, type Resolver, type UseFormReturn } from "react-hook-form";
import { useTranslation } from "react-i18next";
import i18n from "@/lib/i18n";

// ProfileForm is a single profile's form shape, derived from the form schema.
type ProfileForm = OtelFormSchema["profiles"][number];

// StoredOtelProfile is one profile as persisted/returned by the API (headers are strings,
// SecretVar fields may be plain strings or full objects).
interface StoredOtelProfile {
	enabled?: boolean;
	service_name?: string;
	collector_url?: string | SecretVar;
	headers?: Record<string, string | SecretVar>;
	trace_type?: "genai_extension" | "vercel" | "open_inference";
	protocol?: "http" | "grpc";
	tls_ca_cert?: string;
	insecure?: boolean;
	metrics_enabled?: boolean;
	metrics_endpoint?: string | SecretVar;
	metrics_push_interval?: number;
	export_timeout?: number;
	request_headers?: string[];
	disable_content_logging?: boolean;
	group_traces_by_session?: boolean;
	disable_root_span_content?: boolean;
}

type StoredSelectiveExport = OtelFormSchema["selective_export"];

// StoredOtelConfig is either the canonical { profiles: [...] } wrapper or a legacy single
// profile object (no "profiles" key).
type StoredOtelConfig =
	| (StoredOtelProfile & {
			profiles?: StoredOtelProfile[];
			selective_export?: StoredSelectiveExport;
	  })
	| undefined;

interface OtelFormFragmentProps {
	currentConfig?: {
		enabled?: boolean;
		config?: StoredOtelConfig;
	};
	onSave: (config: OtelFormSchema) => Promise<void>;
	onDelete?: () => void;
	isDeleting?: boolean;
	isLoading?: boolean;
}

const traceTypeOptions: {
	value: string;
	label: string;
	disabled?: boolean;
	disabledReason?: string;
}[] = [
	{ value: "genai_extension", label: "OTel GenAI Extension (Recommended)" },
	{
		value: "vercel",
		label: "Vercel AI SDK",
		disabled: true,
		disabledReason: "Coming soon",
	},
	{
		value: "open_inference",
		label: "Arize OpenInference",
		disabled: true,
		disabledReason: "Coming soon",
	},
];
const protocolOptions: {
	value: string;
	label: string;
	disabled?: boolean;
	disabledReason?: string;
}[] = [
	{ value: "http", label: "HTTP" },
	{ value: "grpc", label: "GRPC" },
];

// emptyProfile returns a fresh profile with the same defaults a newly created collector uses.
const emptyProfile = (): ProfileForm => ({
	enabled: true,
	service_name: "bifrost",
	collector_url: emptySecretVar(),
	headers: {},
	trace_type: "genai_extension",
	protocol: "http",
	tls_ca_cert: "",
	insecure: true,
	metrics_enabled: false,
	metrics_endpoint: emptySecretVar(),
	metrics_push_interval: 15,
	export_timeout: 5,
	request_headers: [],
	disable_content_logging: false,
	group_traces_by_session: false,
	disable_root_span_content: false,
});

const defaultSelectionRules = (): StoredSelectiveExport["rules"] => [
	{
		id: "errors",
		priority: 100,
		request_types: [],
		require_error: true,
		error_categories: [],
		providers: [],
		models: [],
		routing_rules: [],
		export_rate: 1,
		max_per_minute: 100,
	},
	{
		id: "fallbacks",
		priority: 90,
		request_types: [],
		require_error: false,
		require_fallback: true,
		error_categories: [],
		providers: [],
		models: [],
		routing_rules: [],
		export_rate: 0.5,
		max_per_minute: 100,
	},
	{
		id: "slow",
		priority: 80,
		request_types: [],
		require_error: false,
		min_latency_ms: 30000,
		error_categories: [],
		providers: [],
		models: [],
		routing_rules: [],
		export_rate: 0.3,
		max_per_minute: 100,
	},
	{
		id: "complete-success",
		priority: 70,
		request_types: [],
		require_error: false,
		min_technical_quality: 0.85,
		error_categories: [],
		providers: [],
		models: [],
		routing_rules: [],
		export_rate: 0.1,
		max_per_minute: 100,
	},
	{
		id: "default",
		priority: 0,
		request_types: [],
		error_categories: [],
		providers: [],
		models: [],
		routing_rules: [],
		export_rate: 0.01,
		max_per_minute: 50,
	},
];

const defaultSelectiveExport = (): StoredSelectiveExport => ({
	enabled: false,
	dry_run: true,
	require_complete_record: true,
	candidate_rate: 1,
	max_exports_per_minute: 500,
	rules: defaultSelectionRules(),
});

const orderedSelectiveExport = (selection?: StoredSelectiveExport): StoredSelectiveExport => {
	if (!selection) return defaultSelectiveExport();
	return { ...selection, rules: [...(selection.rules ?? [])].sort((left, right) => right.priority - left.priority) };
};

// toProfileForm normalizes a stored profile into the SecretVar-based form representation.
const toProfileForm = (p?: StoredOtelProfile): ProfileForm => ({
	enabled: p?.enabled ?? true,
	service_name: p?.service_name ?? "bifrost",
	collector_url: toSecretVarFormValue(p?.collector_url),
	headers: toSecretVarMapFormValue(p?.headers),
	trace_type: p?.trace_type ?? "genai_extension",
	protocol: p?.protocol ?? "http",
	tls_ca_cert: p?.tls_ca_cert ?? "",
	insecure: p?.insecure ?? true,
	metrics_enabled: p?.metrics_enabled ?? false,
	metrics_endpoint: toSecretVarFormValue(p?.metrics_endpoint),
	metrics_push_interval: p?.metrics_push_interval ?? 15,
	export_timeout: p?.export_timeout ?? 5,
	request_headers: p?.request_headers ?? [],
	disable_content_logging: p?.disable_content_logging ?? false,
	group_traces_by_session: p?.group_traces_by_session ?? false,
	disable_root_span_content: p?.disable_root_span_content ?? false,
});

// buildDefaults handles both stored shapes: the { profiles: [...] } wrapper and the legacy
// single-object config. Always yields at least one profile.
const buildDefaults = (initial?: OtelFormFragmentProps["currentConfig"]): OtelFormSchema => {
	const cfg = initial?.config;
	let profiles: ProfileForm[];
	if (cfg && Array.isArray(cfg.profiles)) {
		profiles = cfg.profiles.map(toProfileForm);
	} else if (cfg && (cfg.collector_url || cfg.service_name || cfg.protocol || cfg.trace_type)) {
		// Legacy single-object config.
		profiles = [toProfileForm(cfg)];
	} else {
		profiles = [];
	}
	if (profiles.length === 0) profiles = [emptyProfile()];
	return {
		enabled: initial?.enabled ?? true,
		profiles,
		selective_export: orderedSelectiveExport(cfg?.selective_export),
	};
};

export function OtelFormFragment({
	currentConfig: initialConfig,
	onSave,
	onDelete,
	isDeleting = false,
	isLoading = false,
}: OtelFormFragmentProps) {
	const { t } = useTranslation();
	const hasOtelAccess = useRbac(RbacResource.Observability, RbacOperation.Update);
	const [isSaving, setIsSaving] = useState(false);
	const [profileOpenState, setProfileOpenState] = useState<Record<number, boolean>>({});
	const form = useForm<OtelFormSchema, unknown, OtelFormSchema>({
		resolver: zodResolver(otelFormSchema) as Resolver<OtelFormSchema, unknown, OtelFormSchema>,
		mode: "onChange",
		reValidateMode: "onChange",
		defaultValues: buildDefaults(initialConfig),
	});

	const { fields, append, remove } = useFieldArray({
		control: form.control,
		name: "profiles",
	});

	const onSubmit = (data: OtelFormSchema) => {
		setIsSaving(true);
		const rules = data.selective_export.rules;
		const normalized = {
			...data,
			selective_export: {
				...data.selective_export,
				rules: rules.map((rule, index) => ({ ...rule, priority: (rules.length - index) * 10 })),
			},
		};
		onSave(normalized).finally(() => setIsSaving(false));
	};

	const handleProfileOpenChange = (index: number, open: boolean) => {
		setProfileOpenState((prev) => ({ ...prev, [index]: open }));
	};

	const handleRemoveProfile = (index: number) => {
		remove(index);
		setProfileOpenState((prev) => {
			const next: Record<number, boolean> = {};
			for (const [key, value] of Object.entries(prev)) {
				const profileIndex = Number(key);
				if (profileIndex < index) {
					next[profileIndex] = value;
				} else if (profileIndex > index) {
					next[profileIndex - 1] = value;
				}
			}
			return next;
		});
	};

	useEffect(() => {
		form.reset(buildDefaults(initialConfig));
	}, [form, initialConfig]);

	return (
		<Form {...form}>
			<form onSubmit={form.handleSubmit(onSubmit)} className="space-y-6">
				<div className="flex flex-col gap-3">
					{fields.map((field, index) => (
						<OtelProfileSection
							key={field.id}
							form={form}
							control={form.control}
							index={index}
							hasOtelAccess={hasOtelAccess}
							canRemove={fields.length > 1}
							open={profileOpenState[index] ?? true}
							onOpenChange={(open) => handleProfileOpenChange(index, open)}
							onRemove={() => handleRemoveProfile(index)}
						/>
					))}
				</div>

				<Button
					type="button"
					variant="outline"
					size="sm"
					onClick={() => append(emptyProfile())}
					disabled={!hasOtelAccess}
					data-testid="otel-add-profile-btn"
				>
					<Plus className="size-4" /> {t("workspace.observability.otelForm.addProfile")}
				</Button>

				<SelectiveExportSection form={form} hasOtelAccess={hasOtelAccess} />

				{/* Form Actions */}
				<div className="flex w-full flex-row items-center border-t pt-4">
					<FormField
						control={form.control}
						name="enabled"
						render={({ field }) => (
							<FormItem className="flex items-center gap-2 py-2">
								<FormLabel className="text-muted-foreground text-sm font-medium">{t("workspace.observability.otelForm.enabled")}</FormLabel>
								<FormControl>
									<Switch
										checked={field.value}
										onCheckedChange={field.onChange}
										disabled={!hasOtelAccess}
										data-testid="otel-connector-enable-toggle"
									/>
								</FormControl>
							</FormItem>
						)}
					/>
					<div className="ml-auto flex justify-end space-x-2 py-2">
						{onDelete && (
							<Button
								type="button"
								variant="outline"
								onClick={onDelete}
								disabled={isDeleting || !hasOtelAccess}
								data-testid="otel-connector-delete-btn"
								title={t("workspace.observability.otelForm.deleteConnector")}
								aria-label={t("workspace.observability.otelForm.deleteConnector")}
							>
								<Trash2 className="size-4" />
							</Button>
						)}
						<Button
							type="button"
							variant="outline"
							onClick={() => {
								form.reset(buildDefaults(initialConfig));
							}}
							disabled={!hasOtelAccess || isLoading || !form.formState.isDirty}
						>
							{i18n.t("workspace.plugins.reset")}
						</Button>
						<TooltipProvider>
							<Tooltip>
								<TooltipTrigger asChild>
									<Button type="submit" disabled={!hasOtelAccess || !form.formState.isDirty} isLoading={isSaving}>
										{t("workspace.observability.otelForm.save")}
									</Button>
								</TooltipTrigger>
								{!form.formState.isDirty && (
									<TooltipContent>
										<p>
											{!form.formState.isDirty && !form.formState.isValid
												? "No changes made and validation errors present"
												: !form.formState.isDirty
													? "No changes made"
													: "Please fix validation errors"}
										</p>
									</TooltipContent>
								)}
							</Tooltip>
						</TooltipProvider>
					</div>
				</div>
			</form>
		</Form>
	);
}

interface OtelProfileSectionProps {
	form: UseFormReturn<OtelFormSchema, unknown, OtelFormSchema>;
	control: Control<OtelFormSchema, unknown, OtelFormSchema>;
	index: number;
	hasOtelAccess: boolean;
	canRemove: boolean;
	open: boolean;
	onOpenChange: (open: boolean) => void;
	onRemove: () => void;
}

function SelectionFieldLabel({ label, description }: { label: string; description: string }) {
	const { t } = useTranslation();
	return (
		<TooltipProvider delayDuration={200}>
			<div className="flex items-center gap-1.5">
				<FormLabel>{label}</FormLabel>
				<Tooltip>
					<TooltipTrigger asChild>
						<button
							type="button"
							className="text-muted-foreground hover:text-foreground"
							aria-label={t("workspace.observability.otelForm.selective.aboutField", { label })}
						>
							<Info className="size-3.5" aria-hidden="true" />
						</button>
					</TooltipTrigger>
					<TooltipContent className="max-w-xs leading-relaxed">{description}</TooltipContent>
				</Tooltip>
			</div>
		</TooltipProvider>
	);
}

type SelectionRule = StoredSelectiveExport["rules"][number];

const csvValues = (value: string) =>
	value
		.split(",")
		.map((item) => item.trim())
		.filter(Boolean);
const isCatchAllRule = (rule: SelectionRule | undefined) =>
	!!rule &&
	!rule.request_types?.length &&
	rule.require_error === undefined &&
	rule.require_fallback === undefined &&
	rule.require_retry === undefined &&
	!rule.error_categories?.length &&
	!rule.providers?.length &&
	!rule.models?.length &&
	!rule.routing_rules?.length &&
	rule.min_latency_ms === undefined &&
	rule.max_latency_ms === undefined &&
	rule.min_technical_quality === undefined &&
	rule.min_cost === undefined;

function SelectionRuleCard({
	form,
	fieldID,
	index,
	rule,
	hasOtelAccess,
	onRemove,
}: {
	form: UseFormReturn<OtelFormSchema, unknown, OtelFormSchema>;
	fieldID: string;
	index: number;
	rule: SelectionRule;
	hasOtelAccess: boolean;
	onRemove: () => void;
}) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const [advancedOpen, setAdvancedOpen] = useState(false);
	const catchAll = isCatchAllRule(rule);
	const { ref, handleRef, isDragging } = useSortable({ id: fieldID, index, disabled: catchAll || !hasOtelAccess });
	const base = `selective_export.rules.${index}` as const;
	const conditions: string[] = [];
	const requestType = rule.request_types?.[0];
	if (requestType) conditions.push(t(`workspace.observability.otelForm.selective.summary.${requestType}`));
	if (rule.require_error === true) conditions.push(t("workspace.observability.otelForm.selective.summary.failed"));
	if (rule.require_error === false) conditions.push(t("workspace.observability.otelForm.selective.summary.succeeded"));
	if (rule.require_fallback === true) conditions.push(t("workspace.observability.otelForm.selective.summary.usedFallback"));
	if (rule.require_fallback === false) conditions.push(t("workspace.observability.otelForm.selective.summary.primaryRoute"));
	if (rule.require_retry === true) conditions.push(t("workspace.observability.otelForm.selective.summary.retried"));
	if (rule.error_categories?.[0])
		conditions.push(t(`workspace.observability.otelForm.selective.errorCategories.${rule.error_categories[0]}`));
	if (rule.min_latency_ms !== undefined)
		conditions.push(t("workspace.observability.otelForm.selective.summary.minLatency", { value: rule.min_latency_ms }));
	if (rule.max_latency_ms !== undefined)
		conditions.push(t("workspace.observability.otelForm.selective.summary.maxLatency", { value: rule.max_latency_ms }));
	if (rule.min_technical_quality !== undefined)
		conditions.push(t("workspace.observability.otelForm.selective.summary.minCompleteness", { value: rule.min_technical_quality }));
	if (rule.min_cost !== undefined)
		conditions.push(t("workspace.observability.otelForm.selective.summary.minCost", { value: rule.min_cost }));
	if (rule.providers?.length)
		conditions.push(t("workspace.observability.otelForm.selective.summary.providers", { value: rule.providers.join(", ") }));
	if (rule.models?.length)
		conditions.push(t("workspace.observability.otelForm.selective.summary.models", { value: rule.models.join(", ") }));
	if (rule.routing_rules?.length)
		conditions.push(t("workspace.observability.otelForm.selective.summary.routingRules", { value: rule.routing_rules.join(", ") }));

	const triState = (value: boolean | undefined) => (value === undefined ? "any" : String(value));
	const updateTriState = (onChange: (value: boolean | undefined) => void, value: string) =>
		onChange(value === "any" ? undefined : value === "true");

	return (
		<div
			ref={ref}
			className={cn("rounded-sm border bg-card", isDragging && "opacity-50")}
			data-testid={`otel-selective-export-rule-${index}`}
		>
			<div className="flex items-center gap-3 px-3 py-3">
				{catchAll ? (
					<div className="flex size-5 items-center justify-center">
						<CheckCircle2 className="text-muted-foreground size-4" />
					</div>
				) : (
					<button
						ref={handleRef}
						type="button"
						className="text-muted-foreground cursor-grab p-0.5 active:cursor-grabbing"
						aria-label={t("workspace.observability.otelForm.selective.reorder")}
					>
						<GripVertical className="size-4" />
					</button>
				)}
				<button type="button" className="min-w-0 flex-1 text-left" onClick={() => setOpen((value) => !value)}>
					<div className="flex flex-wrap items-center gap-2">
						<span className="text-sm font-medium">
							{catchAll
								? t("workspace.observability.otelForm.selective.otherRequests")
								: t("workspace.observability.otelForm.selective.when")}
						</span>
						<span className="text-muted-foreground truncate text-sm">
							{catchAll
								? t("workspace.observability.otelForm.selective.noEarlierMatch")
								: conditions.join(` ${t("workspace.observability.otelForm.selective.and")} `) ||
									t("workspace.observability.otelForm.selective.allImageRequests")}
						</span>
					</div>
					<div className="text-muted-foreground mt-1 text-xs">
						{t("workspace.observability.otelForm.selective.thenExport", {
							rate: Math.round((rule.export_rate ?? 0) * 10000) / 100,
							max: rule.max_per_minute || t("workspace.observability.otelForm.selective.unlimited"),
						})}
					</div>
				</button>
				<Badge variant="outline">{Math.round((rule.export_rate ?? 0) * 10000) / 100}%</Badge>
				{!catchAll && (
					<Button
						type="button"
						variant="ghost"
						size="icon"
						onClick={onRemove}
						disabled={!hasOtelAccess}
						data-testid={`otel-selective-export-rule-${index}-remove`}
						aria-label={t("workspace.observability.otelForm.selective.remove")}
					>
						<Trash2 className="size-4" />
					</Button>
				)}
				<Button
					type="button"
					variant="ghost"
					size="icon"
					onClick={() => setOpen((value) => !value)}
					aria-label={t("workspace.observability.otelForm.selective.editPolicy")}
				>
					<ChevronDown className={cn("size-4 transition-transform", open && "rotate-180")} />
				</Button>
			</div>

			{open && (
				<div className="space-y-4 border-t p-4">
					{!catchAll && (
						<>
							<div>
								<p className="mb-3 text-sm font-medium">{t("workspace.observability.otelForm.selective.whenAllConditions")}</p>
								<div className="grid gap-3 md:grid-cols-4">
									<FormField
										control={form.control}
										name={`${base}.request_types`}
										render={({ field }) => (
											<FormItem>
												<SelectionFieldLabel
													label={t("workspace.observability.otelForm.selective.requestType")}
													description={t("workspace.observability.otelForm.selective.requestTypeHelp")}
												/>
												<Select
													value={field.value?.[0] ?? "all"}
													onValueChange={(value) => field.onChange(value === "all" ? [] : [value])}
													disabled={!hasOtelAccess}
												>
													<FormControl>
														<SelectTrigger data-testid={`otel-selective-export-rule-${index}-request-type`}>
															<SelectValue />
														</SelectTrigger>
													</FormControl>
													<SelectContent>
														<SelectItem value="all">{t("workspace.observability.otelForm.selective.allImageRequests")}</SelectItem>
														<SelectItem value="image_generation">{t("workspace.observability.otelForm.selective.generation")}</SelectItem>
														<SelectItem value="image_edit">{t("workspace.observability.otelForm.selective.edit")}</SelectItem>
														<SelectItem value="image_variation">{t("workspace.observability.otelForm.selective.variation")}</SelectItem>
													</SelectContent>
												</Select>
											</FormItem>
										)}
									/>
									<FormField
										control={form.control}
										name={`${base}.require_error`}
										render={({ field }) => (
											<FormItem>
												<SelectionFieldLabel
													label={t("workspace.observability.otelForm.selective.result")}
													description={t("workspace.observability.otelForm.selective.errorHelp")}
												/>
												<Select
													value={triState(field.value)}
													onValueChange={(value) => {
														updateTriState(field.onChange, value);
														if (value === "false") form.setValue(`${base}.error_categories`, []);
													}}
													disabled={!hasOtelAccess}
												>
													<FormControl>
														<SelectTrigger data-testid={`otel-selective-export-rule-${index}-require-error`}>
															<SelectValue />
														</SelectTrigger>
													</FormControl>
													<SelectContent>
														<SelectItem value="any">{t("workspace.observability.otelForm.selective.any")}</SelectItem>
														<SelectItem value="false">{t("workspace.observability.otelForm.selective.success")}</SelectItem>
														<SelectItem value="true">{t("workspace.observability.otelForm.selective.failure")}</SelectItem>
													</SelectContent>
												</Select>
											</FormItem>
										)}
									/>
									<FormField
										control={form.control}
										name={`${base}.require_fallback`}
										render={({ field }) => (
											<FormItem>
												<SelectionFieldLabel
													label={t("workspace.observability.otelForm.selective.fallback")}
													description={t("workspace.observability.otelForm.selective.fallbackHelp")}
												/>
												<Select
													value={triState(field.value)}
													onValueChange={(value) => updateTriState(field.onChange, value)}
													disabled={!hasOtelAccess}
												>
													<FormControl>
														<SelectTrigger data-testid={`otel-selective-export-rule-${index}-require-fallback`}>
															<SelectValue />
														</SelectTrigger>
													</FormControl>
													<SelectContent>
														<SelectItem value="any">{t("workspace.observability.otelForm.selective.any")}</SelectItem>
														<SelectItem value="true">{t("workspace.observability.otelForm.selective.yes")}</SelectItem>
														<SelectItem value="false">{t("workspace.observability.otelForm.selective.no")}</SelectItem>
													</SelectContent>
												</Select>
											</FormItem>
										)}
									/>
									<FormField
										control={form.control}
										name={`${base}.require_retry`}
										render={({ field }) => (
											<FormItem>
												<SelectionFieldLabel
													label={t("workspace.observability.otelForm.selective.retry")}
													description={t("workspace.observability.otelForm.selective.retryHelp")}
												/>
												<Select
													value={triState(field.value)}
													onValueChange={(value) => updateTriState(field.onChange, value)}
													disabled={!hasOtelAccess}
												>
													<FormControl>
														<SelectTrigger data-testid={`otel-selective-export-rule-${index}-require-retry`}>
															<SelectValue />
														</SelectTrigger>
													</FormControl>
													<SelectContent>
														<SelectItem value="any">{t("workspace.observability.otelForm.selective.any")}</SelectItem>
														<SelectItem value="true">{t("workspace.observability.otelForm.selective.yes")}</SelectItem>
														<SelectItem value="false">{t("workspace.observability.otelForm.selective.no")}</SelectItem>
													</SelectContent>
												</Select>
											</FormItem>
										)}
									/>
								</div>
							</div>

							<Collapsible open={advancedOpen} onOpenChange={setAdvancedOpen}>
								<CollapsibleTrigger asChild>
									<Button type="button" variant="ghost" size="sm" className="px-0">
										<Settings2 className="size-4" /> {t("workspace.observability.otelForm.selective.moreConditions")}
										<ChevronDown className={cn("size-4 transition-transform", advancedOpen && "rotate-180")} />
									</Button>
								</CollapsibleTrigger>
								<CollapsibleContent className="grid gap-3 pt-3 md:grid-cols-4">
									<FormField
										control={form.control}
										name={`${base}.error_categories`}
										render={({ field }) => (
											<FormItem>
												<SelectionFieldLabel
													label={t("workspace.observability.otelForm.selective.errorCategory")}
													description={t("workspace.observability.otelForm.selective.errorCategoryHelp")}
												/>
												<Select
													value={field.value?.[0] ?? "any"}
													onValueChange={(value) => {
														field.onChange(value === "any" ? [] : [value]);
														if (value !== "any") form.setValue(`${base}.require_error`, true);
													}}
													disabled={!hasOtelAccess}
												>
													<FormControl>
														<SelectTrigger>
															<SelectValue />
														</SelectTrigger>
													</FormControl>
													<SelectContent>
														<SelectItem value="any">{t("workspace.observability.otelForm.selective.any")}</SelectItem>
														{["timeout", "connection", "client_error", "server_error", "other"].map((value) => (
															<SelectItem key={value} value={value}>
																{t(`workspace.observability.otelForm.selective.errorCategories.${value}`)}
															</SelectItem>
														))}
													</SelectContent>
												</Select>
											</FormItem>
										)}
									/>
									{(["providers", "models", "routing_rules"] as const).map((key) => (
										<FormField
											key={key}
											control={form.control}
											name={`${base}.${key}`}
											render={({ field }) => (
												<FormItem>
													<SelectionFieldLabel
														label={t(`workspace.observability.otelForm.selective.${key}`)}
														description={t(`workspace.observability.otelForm.selective.${key}Help`)}
													/>
													<FormControl>
														<Input
															value={field.value?.join(", ") ?? ""}
															onChange={(event) => field.onChange(csvValues(event.target.value))}
															placeholder={t("workspace.observability.otelForm.selective.commaSeparated")}
															disabled={!hasOtelAccess}
														/>
													</FormControl>
												</FormItem>
											)}
										/>
									))}
									{(["min_latency_ms", "max_latency_ms", "min_technical_quality", "min_cost"] as const).map((key) => (
										<FormField
											key={key}
											control={form.control}
											name={`${base}.${key}`}
											render={({ field }) => (
												<FormItem>
													<SelectionFieldLabel
														label={t(`workspace.observability.otelForm.selective.${key}`)}
														description={t(`workspace.observability.otelForm.selective.${key}Help`)}
													/>
													<FormControl>
														<Input
															type="number"
															min={0}
															max={key === "min_technical_quality" ? 1 : undefined}
															step={key === "min_cost" ? 0.001 : key === "min_technical_quality" ? 0.05 : 1}
															value={field.value ?? ""}
															onChange={(event) => field.onChange(event.target.value === "" ? undefined : Number(event.target.value))}
															placeholder={t("workspace.observability.otelForm.selective.any")}
															disabled={!hasOtelAccess}
														/>
													</FormControl>
													<FormMessage />
												</FormItem>
											)}
										/>
									))}
								</CollapsibleContent>
							</Collapsible>
						</>
					)}

					<div className="bg-muted/40 rounded-sm p-3">
						<p className="mb-3 text-sm font-medium">{t("workspace.observability.otelForm.selective.then")}</p>
						<div className="grid gap-3 md:grid-cols-2">
							<FormField
								control={form.control}
								name={`${base}.export_rate`}
								render={({ field }) => (
									<FormItem>
										<SelectionFieldLabel
											label={t("workspace.observability.otelForm.selective.exportRate")}
											description={t("workspace.observability.otelForm.selective.exportRateHelp")}
										/>
										<FormControl>
											<Input
												type="number"
												min={0}
												max={100}
												step={0.1}
												disabled={!hasOtelAccess}
												data-testid={`otel-selective-export-rule-${index}-export-rate`}
												value={Math.round((field.value ?? 0) * 10000) / 100}
												onChange={(event) => field.onChange(Number(event.target.value) / 100)}
											/>
										</FormControl>
										<FormMessage />
									</FormItem>
								)}
							/>
							<FormField
								control={form.control}
								name={`${base}.max_per_minute`}
								render={({ field }) => (
									<FormItem>
										<SelectionFieldLabel
											label={t("workspace.observability.otelForm.selective.ruleMax")}
											description={t("workspace.observability.otelForm.selective.ruleMaxHelp")}
										/>
										<FormControl>
											<Input
												type="number"
												min={0}
												max={10000}
												disabled={!hasOtelAccess}
												data-testid={`otel-selective-export-rule-${index}-max-per-minute`}
												{...field}
												onChange={(event) => field.onChange(Number(event.target.value))}
											/>
										</FormControl>
										<FormMessage />
									</FormItem>
								)}
							/>
						</div>
					</div>
				</div>
			)}
		</div>
	);
}

function SelectiveExportSection({
	form,
	hasOtelAccess,
}: {
	form: UseFormReturn<OtelFormSchema, unknown, OtelFormSchema>;
	hasOtelAccess: boolean;
}) {
	const { t } = useTranslation();
	const enabled = form.watch("selective_export.enabled");
	const rules = form.watch("selective_export.rules");
	const [advancedOpen, setAdvancedOpen] = useState(false);
	const { fields, insert, remove, move } = useFieldArray({ control: form.control, name: "selective_export.rules" });
	const catchAllIndex = rules.findIndex(isCatchAllRule);
	const addPolicy = () => {
		const next = {
			id: `policy-${Date.now()}-${fields.length + 1}`,
			priority: 0,
			request_types: ["image_generation" as const],
			error_categories: [],
			providers: [],
			models: [],
			routing_rules: [],
			export_rate: 0.1,
			max_per_minute: 50,
		};
		insert(catchAllIndex >= 0 ? catchAllIndex : fields.length, next);
	};
	const addBalancedSampling = () => {
		const createdAt = Date.now();
		const balanced = [
			{ type: "image_generation" as const, rate: 0.05 },
			{ type: "image_edit" as const, rate: 0.2 },
			{ type: "image_variation" as const, rate: 0.3 },
		].map(({ type, rate }, index) => ({
			id: `balanced-${type}-${createdAt}-${index}`,
			priority: 0,
			request_types: [type],
			require_error: false,
			require_fallback: false,
			error_categories: [],
			providers: [],
			models: [],
			routing_rules: [],
			export_rate: rate,
			max_per_minute: 50,
		}));
		insert(catchAllIndex >= 0 ? catchAllIndex : fields.length, balanced);
	};
	return (
		<div className="space-y-4 rounded-sm border p-4" data-testid="otel-selective-export-section">
			<div className="flex items-center justify-between gap-4">
				<div>
					<FormLabel className="text-base">{t("workspace.observability.otelForm.selective.title")}</FormLabel>
					<FormDescription>{t("workspace.observability.otelForm.selective.description")}</FormDescription>
				</div>
				<FormField
					control={form.control}
					name="selective_export.enabled"
					render={({ field }) => (
						<FormItem>
							<FormControl>
								<Switch
									checked={field.value}
									onCheckedChange={field.onChange}
									disabled={!hasOtelAccess}
									data-testid="otel-selective-export-enable-toggle"
								/>
							</FormControl>
						</FormItem>
					)}
				/>
			</div>
			{enabled && (
				<>
					<div className="grid gap-3 md:grid-cols-2">
						<FormField
							control={form.control}
							name="selective_export.dry_run"
							render={({ field }) => (
								<FormItem className="flex items-center justify-between rounded-sm border p-3">
									<div>
										<FormLabel>{t("workspace.observability.otelForm.selective.dryRun")}</FormLabel>
										<FormDescription>{t("workspace.observability.otelForm.selective.dryRunDescription")}</FormDescription>
									</div>
									<FormControl>
										<Switch
											checked={field.value}
											onCheckedChange={field.onChange}
											disabled={!hasOtelAccess}
											data-testid="otel-selective-export-dry-run-toggle"
										/>
									</FormControl>
								</FormItem>
							)}
						/>
						<div className="flex items-start gap-3 rounded-sm border p-3">
							<CheckCircle2 className="mt-0.5 size-4 shrink-0 text-emerald-600" />
							<div>
								<FormLabel>{t("workspace.observability.otelForm.selective.atomicEnabled")}</FormLabel>
								<FormDescription>{t("workspace.observability.otelForm.selective.atomicDescription")}</FormDescription>
							</div>
						</div>
					</div>

					<div>
						<div className="mb-2 flex items-end justify-between gap-3">
							<div>
								<FormLabel>{t("workspace.observability.otelForm.selective.policies")}</FormLabel>
								<FormDescription>{t("workspace.observability.otelForm.selective.policiesDescription")}</FormDescription>
							</div>
							<div className="flex flex-wrap justify-end gap-2">
								<Button
									type="button"
									variant="ghost"
									size="sm"
									onClick={addBalancedSampling}
									disabled={!hasOtelAccess || fields.length > 29}
									data-testid="otel-selective-export-balanced-template"
								>
									{t("workspace.observability.otelForm.selective.balancedTemplate")}
								</Button>
								<Button
									type="button"
									variant="outline"
									size="sm"
									onClick={addPolicy}
									disabled={!hasOtelAccess || fields.length >= 32}
									data-testid="otel-selective-export-add-rule"
								>
									<Plus className="size-4" /> {t("workspace.observability.otelForm.selective.addPolicy")}
								</Button>
							</div>
						</div>
						<DragDropProvider
							onDragOver={(event) => {
								const { source, target } = event.operation;
								if (!source || !target || source.id === target.id) return;
								const sourceIndex = fields.findIndex((field) => field.id === source.id);
								const targetIndex = fields.findIndex((field) => field.id === target.id);
								if (sourceIndex < 0 || targetIndex < 0 || isCatchAllRule(rules[sourceIndex]) || isCatchAllRule(rules[targetIndex])) return;
								move(sourceIndex, targetIndex);
							}}
						>
							<div className="space-y-2">
								{fields.map((field, index) => (
									<SelectionRuleCard
										key={field.id}
										fieldID={field.id}
										index={index}
										rule={rules[index]}
										form={form}
										hasOtelAccess={hasOtelAccess}
										onRemove={() => remove(index)}
									/>
								))}
							</div>
						</DragDropProvider>
					</div>

					<Collapsible open={advancedOpen} onOpenChange={setAdvancedOpen}>
						<CollapsibleTrigger asChild>
							<Button type="button" variant="ghost" size="sm" className="px-0">
								<Settings2 className="size-4" /> {t("workspace.observability.otelForm.selective.advancedSettings")}
								<ChevronDown className={cn("size-4 transition-transform", advancedOpen && "rotate-180")} />
							</Button>
						</CollapsibleTrigger>
						<CollapsibleContent className="grid gap-4 rounded-sm border p-3 md:grid-cols-2">
							<FormField
								control={form.control}
								name="selective_export.candidate_rate"
								render={({ field }) => (
									<FormItem>
										<SelectionFieldLabel
											label={t("workspace.observability.otelForm.selective.mediaCandidate")}
											description={t("workspace.observability.otelForm.selective.mediaCandidateHelp")}
										/>
										<FormControl>
											<Input
												type="number"
												min={0}
												max={100}
												step={0.1}
												disabled={!hasOtelAccess}
												data-testid="otel-selective-export-candidate-rate"
												value={Math.round((field.value ?? 0) * 10000) / 100}
												onChange={(event) => field.onChange(Number(event.target.value) / 100)}
											/>
										</FormControl>
										<FormDescription>{t("workspace.observability.otelForm.selective.mediaCandidateDescription")}</FormDescription>
										<FormMessage />
									</FormItem>
								)}
							/>
							<FormField
								control={form.control}
								name="selective_export.max_exports_per_minute"
								render={({ field }) => (
									<FormItem>
										<SelectionFieldLabel
											label={t("workspace.observability.otelForm.selective.processMax")}
											description={t("workspace.observability.otelForm.selective.processMaxHelp")}
										/>
										<FormControl>
											<Input
												type="number"
												min={0}
												max={10000}
												disabled={!hasOtelAccess}
												data-testid="otel-selective-export-process-limit"
												{...field}
												onChange={(event) => field.onChange(Number(event.target.value))}
											/>
										</FormControl>
										<FormDescription>{t("workspace.observability.otelForm.selective.processMaxDescription")}</FormDescription>
										<FormMessage />
									</FormItem>
								)}
							/>
						</CollapsibleContent>
					</Collapsible>
				</>
			)}
		</div>
	);
}

// OtelProfileSection renders one collapsible profile. The header stays visible when collapsed
// and surfaces the profile identity plus its enable toggle and remove control.
function OtelProfileSection({ form, control, index, hasOtelAccess, canRemove, open, onOpenChange, onRemove }: OtelProfileSectionProps) {
	const { t } = useTranslation();
	const base = `profiles.${index}` as const;
	const protocol = form.watch(`${base}.protocol`);
	const metricsEnabled = form.watch(`${base}.metrics_enabled`);
	const insecure = form.watch(`${base}.insecure`);
	const enabled = form.watch(`${base}.enabled`);
	const serviceName = form.watch(`${base}.service_name`);
	const collectorUrl = form.watch(`${base}.collector_url`);

	// Surface whether this profile currently has any validation errors so the user can find it
	// without expanding every collapsed section.
	const hasError = Boolean(form.formState.errors?.profiles?.[index]);

	const collectorPreview =
		typeof collectorUrl === "string"
			? collectorUrl
			: collectorUrl?.type === "env" || collectorUrl?.type === "vault"
				? collectorUrl.ref
				: collectorUrl?.value;

	return (
		<Collapsible open={open} onOpenChange={onOpenChange} className="rounded-sm border" data-testid={`otel-profile-${index}`}>
			<div className="flex flex-row items-center gap-2 px-4 py-3">
				<CollapsibleTrigger asChild>
					<button type="button" className="flex min-w-0 flex-1 items-center gap-2 text-left">
						<ChevronDown className={`size-4 shrink-0 transition-transform ${open ? "" : "-rotate-90"}`} />
						<div className="flex min-w-0 flex-col">
							<span className="flex items-center gap-2 truncate text-sm font-medium">
								{serviceName || `Profile ${index + 1}`}
								{!enabled && <Badge variant="secondary">{i18n.t("workspace.mcp.disabled")}</Badge>}
								{hasError && <Badge variant="destructive">{i18n.t("workspace.mcp.errorTitle")}</Badge>}
							</span>
							{collectorPreview && <span className="text-muted-foreground truncate text-xs">{collectorPreview}</span>}
						</div>
					</button>
				</CollapsibleTrigger>

				<FormField
					control={control}
					name={`${base}.enabled`}
					render={({ field }) => (
						<FormItem className="flex items-center">
							<FormControl>
								<Switch
									checked={field.value}
									onCheckedChange={field.onChange}
									disabled={!hasOtelAccess}
									data-testid={`otel-profile-${index}-enable-toggle`}
									aria-label="Enable profile"
								/>
							</FormControl>
						</FormItem>
					)}
				/>

				{canRemove && (
					<Button
						type="button"
						variant="ghost"
						size="icon"
						onClick={onRemove}
						disabled={!hasOtelAccess}
						data-testid={`otel-profile-${index}-remove-btn`}
						title={i18n.t("workspace.observability.otelForm.removeProfile")}
						aria-label={i18n.t("workspace.observability.otelForm.removeProfile")}
					>
						<Trash2 className="size-4" />
					</Button>
				)}
			</div>

			<CollapsibleContent className="border-t px-4 py-4">
				<div className="flex flex-col gap-4">
					<FormField
						control={control}
						name={`${base}.service_name`}
						render={({ field }) => (
							<FormItem className="w-full">
								<FormLabel>{t("workspace.observability.otelForm.serviceName")}</FormLabel>
								<FormDescription>{t("workspace.observability.otelForm.serviceNameDescription")}</FormDescription>
								<FormControl>
									<Input
										placeholder={i18n.t("workspace.observability.otelForm.serviceNamePlaceholder")}
										disabled={!hasOtelAccess}
										{...field}
									/>
								</FormControl>
								<FormMessage />
							</FormItem>
						)}
					/>
					<FormField
						control={control}
						name={`${base}.collector_url`}
						render={({ field }) => (
							<FormItem className="w-full">
								<FormLabel>{t("workspace.observability.otelForm.collectorUrl")}</FormLabel>
								<div className="text-muted-foreground text-xs">
									<code>{protocol === "http" ? "http(s)://<host>:<port>/v1/traces" : "<host>:<port>"}</code>
								</div>
								<FormControl>
									<SecretVarInput
										placeholder={
											protocol === "http"
												? "https://otel-collector.example.com:4318/v1/traces or env.OTEL_COLLECTOR_URL"
												: "otel-collector.example.com:4317 or env.OTEL_COLLECTOR_URL"
										}
										disabled={!hasOtelAccess}
										{...field}
									/>
								</FormControl>
								<FormMessage />
							</FormItem>
						)}
					/>
					<FormField
						control={control}
						name={`${base}.headers`}
						render={({ field }) => (
							<FormItem className="w-full">
								<FormControl>
									<HeadersTable value={field.value || {}} onChange={field.onChange} disabled={!hasOtelAccess} useSecretVarInput />
								</FormControl>
								<FormMessage />
							</FormItem>
						)}
					/>
					<FormField
						control={control}
						name={`${base}.request_headers`}
						render={({ field }) => (
							<FormItem className="w-full">
								<FormLabel>
									{t("workspace.observability.otelForm.requestHeaders")}{" "}
									<span className="text-muted-foreground font-normal">({t("workspace.observability.otelForm.optional")})</span>
								</FormLabel>
								<FormDescription>
									Comma-separated list of request headers to capture and emit as span attributes. Supports exact names and wildcard patterns
									(e.g. <code className="text-xs">x-custom-*</code> captures all headers with that prefix,{" "}
									<code className="text-xs">*</code> captures all headers; note that <code className="text-xs">*</code> will capture
									sensitive headers like Authorization).
								</FormDescription>
								<FormControl>
									<RequestHeadersTextarea
										className="h-24"
										placeholder="X-Tenant-ID, X-Request-Source, x-custom-*"
										disabled={!hasOtelAccess}
										value={field.value ?? []}
										onChange={field.onChange}
										data-testid={`request-headers-textarea-${index}`}
									/>
								</FormControl>
								<FormMessage />
							</FormItem>
						)}
					/>
					<FormField
						control={control}
						name={`${base}.disable_content_logging`}
						render={({ field }) => (
							<FormItem className="flex flex-row items-center justify-between">
								<div className="space-y-0.5">
									<FormLabel className="text-base">{t("workspace.observability.otelForm.disableContentLogging")}</FormLabel>
									<FormDescription>
										When enabled, message content (input/output messages, tool definitions, and tool call arguments/results) is dropped from
										exported spans. Only metadata such as model, tokens, and latency is sent to the collector.
									</FormDescription>
								</div>
								<FormControl>
									<Switch
										checked={field.value}
										onCheckedChange={field.onChange}
										disabled={!hasOtelAccess}
										data-testid={`otel-profile-${index}-disable-content-logging-toggle`}
									/>
								</FormControl>
							</FormItem>
						)}
					/>
					<FormField
						control={control}
						name={`${base}.group_traces_by_session`}
						render={({ field }) => (
							<FormItem className="flex flex-row items-center justify-between">
								<div className="space-y-0.5">
									<FormLabel className="text-base">{t("workspace.observability.otelForm.groupTracesBySession")}</FormLabel>
									<FormDescription>
										When enabled, requests sharing the same x-bf-session-id header are grouped into a single trace, each request appearing
										as a top-level sibling span. A request carrying an inbound W3C traceparent stays on its own distributed trace and is
										unaffected.
									</FormDescription>
								</div>
								<FormControl>
									<Switch
										checked={field.value}
										onCheckedChange={field.onChange}
										disabled={!hasOtelAccess}
										data-testid={`otel-profile-${index}-group-traces-by-session-toggle`}
									/>
								</FormControl>
							</FormItem>
						)}
					/>
					<FormField
						control={control}
						name={`${base}.disable_root_span_content`}
						render={({ field }) => (
							<FormItem className="flex flex-row items-center justify-between">
								<div className="space-y-0.5">
									<FormLabel className="text-base">{t("workspace.observability.otelForm.disableRootSpanContent")}</FormLabel>
									<FormDescription>
										When enabled, input/output message content is dropped from the root span only; the underlying generation (llm.call) span
										keeps the full content.
									</FormDescription>
								</div>
								<FormControl>
									<Switch
										checked={field.value}
										onCheckedChange={field.onChange}
										disabled={!hasOtelAccess}
										data-testid={`otel-profile-${index}-disable-root-span-content-toggle`}
									/>
								</FormControl>
							</FormItem>
						)}
					/>
					<div className="flex flex-row gap-4">
						<FormField
							control={control}
							name={`${base}.trace_type`}
							render={({ field }) => (
								<FormItem className="flex-1">
									<FormLabel>{t("workspace.observability.otelForm.traceType")}</FormLabel>
									<Select onValueChange={field.onChange} value={field.value ?? traceTypeOptions[0].value} disabled={!hasOtelAccess}>
										<FormControl>
											<SelectTrigger className="w-full">
												<SelectValue placeholder="Select trace type" />
											</SelectTrigger>
										</FormControl>
										<SelectContent>
											{traceTypeOptions.map((option) => (
												<SelectItem
													key={option.value}
													value={option.value}
													disabled={option.disabled}
													disabledReason={option.disabledReason}
												>
													{option.value === "genai_extension"
														? t("workspace.observability.otelForm.traceTypeGenAi")
														: option.value === "vercel"
															? t("workspace.observability.otelForm.traceTypeVercel")
															: t("workspace.observability.otelForm.traceTypeOpenInference")}
												</SelectItem>
											))}
										</SelectContent>
									</Select>
									<FormMessage />
								</FormItem>
							)}
						/>

						<FormField
							control={control}
							name={`${base}.protocol`}
							render={({ field }) => (
								<FormItem className="flex-1">
									<FormLabel>{t("workspace.observability.otelForm.protocol")}</FormLabel>
									<Select onValueChange={field.onChange} value={field.value} disabled={!hasOtelAccess}>
										<FormControl>
											<SelectTrigger className="w-full">
												<SelectValue placeholder="Select protocol" />
											</SelectTrigger>
										</FormControl>
										<SelectContent>
											{protocolOptions.map((option) => (
												<SelectItem
													key={option.value}
													value={option.value}
													disabled={option.disabled}
													disabledReason={option.disabledReason}
												>
													{option.value === "http"
														? t("workspace.observability.otelForm.protocolHttp")
														: t("workspace.observability.otelForm.protocolGrpc")}
												</SelectItem>
											))}
										</SelectContent>
									</Select>
									<FormMessage />
								</FormItem>
							)}
						/>
					</div>

					<FormField
						control={control}
						name={`${base}.export_timeout`}
						render={({ field }) => (
							<FormItem className="w-full max-w-xs">
								<FormLabel>{t("workspace.observability.otelForm.exportTimeout")}</FormLabel>
								<FormControl>
									<Input
										type="number"
										min={1}
										max={60}
										disabled={!hasOtelAccess}
										{...field}
										value={field.value ?? ""}
										onChange={(e) => field.onChange(e.target.value === "" ? null : Number(e.target.value))}
									/>
								</FormControl>
								<FormDescription>
									Maximum time for a single trace export (1-60 seconds). Traces are dropped rather than retried past this limit, so an
									unreachable collector cannot slow down request handling.
								</FormDescription>
								<FormMessage />
							</FormItem>
						)}
					/>

					{/* TLS Configuration */}
					<div className="flex flex-col gap-4">
						<FormField
							control={control}
							name={`${base}.insecure`}
							render={({ field }) => (
								<FormItem className="flex flex-row items-center gap-2">
									<div className="flex w-full flex-row items-center gap-2">
										<div className="flex flex-col gap-1">
											<FormLabel>{t("workspace.observability.otelForm.insecure")}</FormLabel>
											<FormDescription>{t("workspace.observability.otelForm.insecureDescription")}</FormDescription>
										</div>
										<div className="ml-auto">
											<Switch
												checked={field.value}
												onCheckedChange={(checked) => {
													field.onChange(checked);
													if (checked) {
														form.setValue(`${base}.tls_ca_cert`, "");
													}
												}}
												disabled={!hasOtelAccess}
											/>
										</div>
									</div>
								</FormItem>
							)}
						/>
						{!insecure && (
							<FormField
								control={control}
								name={`${base}.tls_ca_cert`}
								render={({ field }) => (
									<FormItem className="w-full">
										<FormLabel>{t("workspace.observability.otelForm.tlsCaCertPath")}</FormLabel>
										<FormDescription>{t("workspace.observability.otelForm.tlsCaCertPathDescription")}</FormDescription>
										<FormControl>
											<Input
												placeholder={i18n.t("workspace.observability.otelForm.tlsCaCertPathPlaceholder")}
												disabled={!hasOtelAccess}
												{...field}
											/>
										</FormControl>
										<FormMessage />
									</FormItem>
								)}
							/>
						)}
					</div>

					{/* Metrics Push Configuration */}
					<div className="flex flex-col gap-4 border-t pt-4">
						<FormField
							control={control}
							name={`${base}.metrics_enabled`}
							render={({ field }) => (
								<FormItem className="flex flex-row items-center gap-2">
									<div className="flex w-full flex-row items-center gap-2">
										<div className="flex flex-col gap-1">
											<h3 className="flex flex-row items-center gap-2 text-sm font-medium">
												{t("workspace.observability.otelForm.metricsEnabled")}{" "}
												<Badge variant="secondary">{t("workspace.observability.otelForm.metricsEnabledBeta")}</Badge>
											</h3>
											<p className="text-muted-foreground text-xs">{t("workspace.observability.otelForm.pushMetricsDescription")}</p>
										</div>
										<div className="ml-auto">
											<Switch
												// First profile keeps the legacy testid for existing e2e coverage.
												data-testid={index === 0 ? "otel-metrics-export-toggle" : `otel-profile-${index}-metrics-export-toggle`}
												checked={field.value}
												onCheckedChange={field.onChange}
												disabled={!hasOtelAccess}
											/>
										</div>
									</div>
								</FormItem>
							)}
						/>

						{metricsEnabled && (
							<div className="border-muted flex flex-col gap-4">
								<FormField
									control={control}
									name={`${base}.metrics_endpoint`}
									render={({ field }) => (
										<FormItem className="w-full">
											<FormLabel>{t("workspace.observability.otelForm.metricsEndpoint")}</FormLabel>
											<div className="text-muted-foreground text-xs">
												<code>{protocol === "http" ? "http(s)://<host>:<port>/v1/metrics" : "<host>:<port>"}</code>
											</div>
											<FormControl>
												<SecretVarInput
													placeholder={
														protocol === "http"
															? "https://otel-collector:4318/v1/metrics or env.OTEL_METRICS_ENDPOINT"
															: "otel-collector:4317 or env.OTEL_METRICS_ENDPOINT"
													}
													disabled={!hasOtelAccess}
													{...field}
												/>
											</FormControl>
											<FormMessage />
										</FormItem>
									)}
								/>

								<FormField
									control={control}
									name={`${base}.metrics_push_interval`}
									render={({ field }) => (
										<FormItem className="w-full max-w-xs">
											<FormLabel>{t("workspace.observability.otelForm.pushInterval")}</FormLabel>
											<FormControl>
												<Input
													type="number"
													min={1}
													max={300}
													disabled={!hasOtelAccess}
													{...field}
													value={field.value ?? ""}
													onChange={(e) => field.onChange(e.target.value === "" ? null : Number(e.target.value))}
												/>
											</FormControl>
											<FormDescription>{t("workspace.observability.otelForm.pushIntervalDescription")}</FormDescription>
											<FormMessage />
										</FormItem>
									)}
								/>
							</div>
						)}
					</div>
				</div>
			</CollapsibleContent>
		</Collapsible>
	);
}