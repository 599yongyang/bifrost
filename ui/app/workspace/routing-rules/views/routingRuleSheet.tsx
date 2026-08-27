/**
 * Routing Rule Dialog (Sheet)
 * Create/Edit form for routing rules
 */

import { CustomerSelector } from "@/components/entitySelectors/customerSelector";
import { TeamSelector } from "@/components/entitySelectors/teamSelector";
import { VirtualKeySelector } from "@/components/entitySelectors/virtualKeySelector";
import { Button } from "@/components/ui/button";
import { ComboboxSelect } from "@/components/ui/combobox";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ModelMultiselect } from "@/components/ui/modelMultiselect";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { ProviderIconType, RenderProviderIcon } from "@/lib/constants/icons";
import { getProviderLabel } from "@/lib/constants/logs";
import { getUserPicker } from "@/lib/registries/userPicker";
import { getErrorMessage } from "@/lib/store";
import { useGetAllKeysQuery, useGetProvidersQuery } from "@/lib/store/apis/providersApi";
import { useCreateRoutingRuleMutation, useGetRoutingRulesQuery, useUpdateRoutingRuleMutation } from "@/lib/store/apis/routingRulesApi";
import {
	DEFAULT_ROUTING_ERROR_FALLBACK,
	DEFAULT_ROUTING_ERROR_FALLBACK_SUPPLEMENT,
	DEFAULT_ROUTING_RULE_FORM_DATA,
	DEFAULT_ROUTING_TARGET,
	ROUTING_RULE_SCOPES,
	RoutingRule,
	RoutingErrorFallback,
	RoutingErrorFallbackCategory,
	RoutingErrorFallbackFormData,
	RoutingRuleFormData,
	RoutingTargetFormData,
} from "@/lib/types/routingRules";
import { validateRateLimitAndBudgetRules, validateRoutingRules } from "@/lib/utils/celConverterRouting";
import { switchErrorFallbackMode, toErrorFallbackFormData, toErrorFallbackPayload } from "@/lib/utils/errorFallbackRules";
import { isValidRuleGroupType, normalizeRoutingRuleGroupQuery } from "@/lib/utils/routingRuleGroupQuery";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { AlertTriangle, ChevronDown, GripVertical, Plus, ShieldCheck, SlidersHorizontal, Trash2, X } from "lucide-react";
import { lazy, Suspense, useCallback, useEffect, useState } from "react";
import { useForm } from "react-hook-form";
import { RuleGroupType } from "react-querybuilder";
import { toast } from "sonner";
// Side-effect import: registers the enterprise user picker (no-op in OSS builds).
import "@enterprise/lib/registrations/userPicker";
import i18n from "@/lib/i18n";

interface RoutingRuleDialogProps {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	editingRule?: RoutingRule | null;
	onSuccess?: () => void;
}

const defaultQuery: RuleGroupType = {
	combinator: "and",
	rules: [],
};

type ConditionMode = "builder" | "cel";

const ERROR_FALLBACK_CATEGORY_OPTIONS = [
	"content_policy",
	"unsupported_operation",
	"rate_limit",
	"authentication",
	"billing",
	"permission",
	"timeout",
	"provider_unavailable",
	"network",
	"invalid_request",
	"internal",
	"unknown",
] as const;

function formatErrorFallbackCategoryLabel(category: string) {
	return i18n.t(`workspace.routingRules.errorFallbackCategoryLabels.${category}`, {
		defaultValue: category.replaceAll("_", " "),
	});
}

function toCommaSeparated(values: Array<string | number> | undefined) {
	return (values ?? []).join(", ");
}

function parseCommaSeparatedList(value: string) {
	return value
		.split(",")
		.map((item) => item.trim())
		.filter(Boolean);
}

function parseCommaSeparatedNumbers(value: string) {
	return parseCommaSeparatedList(value)
		.map((item) => Number.parseInt(item, 10))
		.filter((item) => Number.isFinite(item));
}

function normalizeFallbackString(value: string) {
	const parts = value.split("/");
	const provider = parts[0]?.trim() || "";
	const model = parts.slice(1).join("/").trim();
	return provider ? `${provider}/${model}` : "";
}

function normalizedFallbackDedupKey(value: string) {
	return normalizeFallbackString(value).toLowerCase();
}

function isBlankErrorFallbackRule(rule: RoutingErrorFallbackFormData | undefined) {
	if (!rule) {
		return true;
	}
	if (rule.mode === "scenario") {
		return false;
	}
	return (
		!rule.name?.trim() &&
		(rule.fallbacks?.length ?? 0) === 0 &&
		(rule.when.categories?.length ?? 0) === 0 &&
		(rule.when.error_codes?.length ?? 0) === 0 &&
		(rule.when.error_types?.length ?? 0) === 0 &&
		(rule.when.status_codes?.length ?? 0) === 0 &&
		(rule.when.message_contains?.length ?? 0) === 0
	);
}

/**
 * Decides which conditions editor a rule opens in. Rules authored outside the visual
 * builder (e.g. via the API) have a CEL expression but no usable `query`; those open in
 * CEL mode so the expression stays visible and editable instead of being silently cleared.
 */
function initialConditionMode(rule?: RoutingRule | null): ConditionMode {
	if (!rule) {
		return "builder";
	}
	const hasQuery = isValidRuleGroupType(rule.query) && (rule.query.rules?.length ?? 0) > 0;
	if (hasQuery) {
		return "builder";
	}
	return rule.cel_expression?.trim() ? "cel" : "builder";
}

// Lazy-load CEL builder (heavy dependency tree).
const CELRuleBuilderLazy = lazy(() =>
	import("@/app/workspace/routing-rules/components/celBuilder/celRuleBuilder").then((mod) => ({
		default: mod.CELRuleBuilder,
	})),
);
const CELRuleBuilder = (props: React.ComponentProps<typeof CELRuleBuilderLazy>) => (
	<Suspense fallback={<div className="text-sm text-gray-500">{i18n.t("common.loadingCelBuilder")}</div>}>
		<CELRuleBuilderLazy {...props} />
	</Suspense>
);

export function RoutingRuleSheet({ open, onOpenChange, editingRule, onSuccess }: RoutingRuleDialogProps) {
	const { data: rulesData } = useGetRoutingRulesQuery();
	const rules = rulesData?.rules || [];
	const { data: providersData = [] } = useGetProvidersQuery();
	const { data: allKeysData = [] } = useGetAllKeysQuery();
	const [createRoutingRule, { isLoading: isCreating }] = useCreateRoutingRuleMutation();
	const [updateRoutingRule, { isLoading: isUpdating }] = useUpdateRoutingRuleMutation();

	// State for targets and query (managed outside react-hook-form for complex nested structures)
	const [targets, setTargets] = useState<RoutingTargetFormData[]>([{ ...DEFAULT_ROUTING_TARGET }]);
	const [query, setQuery] = useState<RuleGroupType>(defaultQuery);
	const [conditionMode, setConditionMode] = useState<ConditionMode>("builder");
	const [builderKey, setBuilderKey] = useState(0);
	// Server-side CEL compile error, surfaced inline under the CEL editor instead of a toast.
	const [celError, setCelError] = useState<string | null>(null);

	const {
		register,
		handleSubmit,
		setValue,
		watch,
		reset,
		formState: { errors },
	} = useForm<RoutingRuleFormData>({
		defaultValues: DEFAULT_ROUTING_RULE_FORM_DATA,
	});

	const isEditing = !!editingRule;
	const isLoading = isCreating || isUpdating;
	const canCreate = useRbac(RbacResource.RoutingRules, RbacOperation.Create);
	const canUpdate = useRbac(RbacResource.RoutingRules, RbacOperation.Update);
	const hasRequiredAccess = isEditing ? canUpdate : canCreate;
	const enabled = watch("enabled");
	const chainRule = watch("chain_rule");
	const scope = watch("scope");
	const scopeId = watch("scope_id");

	// Registered by the downstream build at module load; undefined in builds
	// without a user directory, which hides the "User" scope option.
	const UserPicker = getUserPicker();
	const fallbacks = watch("fallbacks");
	const errorFallbacks = watch("error_fallbacks");

	// Get available providers from configured providers, plus any provider already
	// referenced by the current targets, existing rules' targets, or rules' fallbacks
	// so edited/removed providers are still visible in the dropdown.
	const availableProviders = Array.from(
		new Set([
			...providersData.map((p) => p.name),
			...(targets.map((t) => t.provider).filter(Boolean) as string[]),
			...(rules.flatMap((r) => r.targets?.map((t) => t.provider).filter(Boolean) ?? []) as string[]),
			...rules.flatMap((r) => (r.fallbacks ?? []).map((f) => f.split("/")[0]?.trim()).filter(Boolean)),
			...rules.flatMap((r) =>
				(r.error_fallbacks ?? []).flatMap((ef) => (ef.fallbacks ?? []).map((f) => f.split("/")[0]?.trim()).filter(Boolean)),
			),
		]),
	);
	const providerOptions = availableProviders.map((prov) => ({
		label: getProviderLabel(prov),
		value: prov,
		icon: <RenderProviderIcon provider={prov as ProviderIconType} size="sm" className="h-4 w-4" />,
	}));

	// Initialize form data when editing rule changes
	useEffect(() => {
		if (editingRule) {
			setValue("id", editingRule.id);
			setValue("name", editingRule.name);
			setValue("description", editingRule.description);
			setValue("cel_expression", editingRule.cel_expression);
			setValue("fallbacks", editingRule.fallbacks || []);
			setValue("error_fallbacks", (editingRule.error_fallbacks || []).map(toErrorFallbackFormData));
			setValue("scope", editingRule.scope);
			setValue("scope_id", editingRule.scope_id || "");
			setValue("priority", editingRule.priority);
			setValue("enabled", editingRule.enabled);
			setValue("chain_rule", editingRule.chain_rule ?? false);
			if (editingRule.targets && editingRule.targets.length > 0) {
				setTargets(
					editingRule.targets.map((t) => ({
						...DEFAULT_ROUTING_TARGET,
						provider: t.provider || "",
						model: t.model || "",
						key_id: t.key_id || "",
						weight: t.weight,
					})),
				);
			} else {
				setTargets([{ ...DEFAULT_ROUTING_TARGET }]);
			}
			// Only react-querybuilder-shaped queries are valid; config may store other JSON under `query`.
			setQuery(normalizeRoutingRuleGroupQuery(editingRule.query));
			setConditionMode(initialConditionMode(editingRule));
			setBuilderKey((prev) => prev + 1);
			setCelError(null);
		} else {
			reset();
			setTargets([{ ...DEFAULT_ROUTING_TARGET }]);
			setQuery(defaultQuery);
			setConditionMode("builder");
			setBuilderKey((prev) => prev + 1);
			setCelError(null);
		}
	}, [editingRule, open, setValue, reset]);

	const handleQueryChange = useCallback(
		(expression: string, newQuery: RuleGroupType) => {
			setValue("cel_expression", expression);
			setQuery(newQuery);
			// Editing the expression clears a stale server-side CEL error.
			setCelError(null);
		},
		[setValue],
	);

	const handleModeChange = useCallback((mode: ConditionMode) => {
		setConditionMode(mode);
		setCelError(null);
	}, []);

	const addTarget = () => {
		const remaining = 1 - targets.reduce((sum, t) => sum + (t.weight || 0), 0);
		setTargets((prev) => [
			...prev,
			{
				...DEFAULT_ROUTING_TARGET,
				weight: Math.max(0, parseFloat(remaining.toFixed(4))),
			},
		]);
	};

	const removeTarget = (index: number) => {
		setTargets((prev) => prev.filter((_, i) => i !== index));
	};

	const updateTarget = (index: number, field: keyof RoutingTargetFormData, value: string | number) => {
		setTargets((prev) => prev.map((t, i) => (i === index ? { ...t, [field]: value } : t)));
	};

	const totalWeight = targets.reduce((sum, t) => sum + (t.weight || 0), 0);

	const setErrorFallbackRules = (next: RoutingErrorFallbackFormData[]) => {
		setValue("error_fallbacks", next, { shouldDirty: true });
	};

	const updateErrorFallbackRule = (index: number, updater: (rule: RoutingErrorFallbackFormData) => RoutingErrorFallbackFormData) => {
		const next = [...(errorFallbacks || [])];
		next[index] = updater(next[index] || { ...DEFAULT_ROUTING_ERROR_FALLBACK });
		setErrorFallbackRules(next);
	};

	const addErrorFallbackRule = () => {
		setErrorFallbackRules([
			...(errorFallbacks || []),
			{
				...DEFAULT_ROUTING_ERROR_FALLBACK,
				name: i18n.t("workspace.routingRules.errorFallbackDefaultRuleName"),
				supplement: { ...DEFAULT_ROUTING_ERROR_FALLBACK_SUPPLEMENT },
				when: { ...DEFAULT_ROUTING_ERROR_FALLBACK.when },
				fallbacks: [""],
			},
		]);
	};

	const removeErrorFallbackRule = (index: number) => {
		setErrorFallbackRules((errorFallbacks || []).filter((_, i) => i !== index));
	};

	const moveErrorFallbackTarget = (ruleIndex: number, fromIndex: number, toIndex: number) => {
		if (fromIndex === toIndex) return;
		updateErrorFallbackRule(ruleIndex, (current) => {
			const fallbacks = [...current.fallbacks];
			const [moved] = fallbacks.splice(fromIndex, 1);
			fallbacks.splice(toIndex, 0, moved);
			return { ...current, fallbacks };
		});
	};

	const onSubmit = (data: RoutingRuleFormData) => {
		setCelError(null);

		// Validate scope_id is required when scope is not global
		if (data.scope !== "global" && !data.scope_id?.trim()) {
			toast.error(
				`${data.scope === "team" ? "Team" : data.scope === "customer" ? "Customer" : data.scope === "user" ? "User" : "Virtual Key"} is required`,
			);
			return;
		}

		// Validate targets
		if (targets.length === 0) {
			toast.error(i18n.t("workspace.routingRules.targetRequired"));
			return;
		}
		for (const t of targets) {
			if (t.weight <= 0) {
				toast.error(i18n.t("workspace.routingRules.weightGreaterThanZero"));
				return;
			}
		}
		if (Math.abs(totalWeight - 1) > 0.001) {
			toast.error(`Target weights must sum to 1, current total: ${totalWeight.toFixed(4)}`);
			return;
		}

		// Builder-only validation: these inspect the visual query, which does not exist in
		// raw-CEL mode. In CEL mode the expression is validated server-side on save instead.
		if (conditionMode === "builder") {
			// Validate regex patterns in routing rules
			const regexErrors = validateRoutingRules(query);
			if (regexErrors.length > 0) {
				toast.error(`Invalid regex pattern:\n${regexErrors.join("\n")}`);
				return;
			}

			// Validate rate limit and budget rules
			const rateLimitErrors = validateRateLimitAndBudgetRules(query);
			if (rateLimitErrors.length > 0) {
				toast.error(`Invalid rule configuration:\n${rateLimitErrors.join("\n")}`);
				return;
			}
		}

		// Filter out incomplete fallbacks (empty provider)
		const validFallbacks = (data.fallbacks || []).filter((fb) => {
			const provider = fb.split("/")[0]?.trim();
			return provider && provider.length > 0;
		});

		const normalizedErrorFallbacks: RoutingErrorFallback[] = (data.error_fallbacks || [])
			.filter((rule) => !isBlankErrorFallbackRule(rule))
			.map(toErrorFallbackPayload);

		for (let i = 0; i < normalizedErrorFallbacks.length; i++) {
			const rule = normalizedErrorFallbacks[i];
			const hasLegacyMatchers =
				!rule.scenario &&
				!!rule.when &&
				((rule.when.categories?.length ?? 0) > 0 ||
					(rule.when.error_codes?.length ?? 0) > 0 ||
					(rule.when.error_types?.length ?? 0) > 0 ||
					(rule.when.status_codes?.length ?? 0) > 0 ||
					(rule.when.message_contains?.length ?? 0) > 0);
			if (!rule.scenario && !hasLegacyMatchers) {
				toast.error(
					i18n.t("workspace.routingRules.errorFallbackMatcherRequired", {
						index: i + 1,
					}),
				);
				return;
			}
			if (rule.scenario && rule.supplement && (rule.supplement.providers?.length ?? 0) > 0) {
				const hasSupplementSignals =
					(rule.supplement.error_codes?.length ?? 0) > 0 ||
					(rule.supplement.error_types?.length ?? 0) > 0 ||
					(rule.supplement.status_codes?.length ?? 0) > 0 ||
					(rule.supplement.message_contains_any?.length ?? 0) > 0;
				if (!hasSupplementSignals) {
					toast.error(
						i18n.t("workspace.routingRules.errorFallbackSupplementSignalRequired", {
							index: i + 1,
						}),
					);
					return;
				}
			}
			if (rule.fallbacks.length === 0) {
				toast.error(
					i18n.t("workspace.routingRules.errorFallbackChainRequired", {
						index: i + 1,
					}),
				);
				return;
			}
			const seenFallbacks = new Set<string>();
			for (const fallback of rule.fallbacks) {
				const dedupKey = normalizedFallbackDedupKey(fallback);
				if (seenFallbacks.has(dedupKey)) {
					toast.error(
						i18n.t("workspace.routingRules.errorFallbackDuplicateTarget", {
							index: i + 1,
						}),
					);
					return;
				}
				seenFallbacks.add(dedupKey);
			}
		}

		const payload = {
			name: data.name,
			description: data.description,
			cel_expression: data.cel_expression,
			targets: targets.map(({ provider, model, key_id, weight }) => ({
				provider: provider || undefined,
				model: model || undefined,
				key_id: key_id || undefined,
				weight,
			})),
			fallbacks: validFallbacks,
			error_fallbacks: normalizedErrorFallbacks,
			scope: data.scope,
			scope_id: data.scope === "global" ? undefined : data.scope_id || undefined,
			priority: data.priority,
			enabled: data.enabled,
			chain_rule: data.chain_rule,
			query: query,
		};

		const submitPromise =
			isEditing && editingRule
				? updateRoutingRule({
						id: editingRule.id,
						data: payload,
					}).unwrap()
				: createRoutingRule(payload).unwrap();

		submitPromise
			.then(() => {
				toast.success(isEditing ? "Routing rule updated successfully" : "Routing rule created successfully");
				reset();
				setTargets([{ ...DEFAULT_ROUTING_TARGET }]);
				setQuery(defaultQuery);
				setConditionMode("builder");
				setBuilderKey((prev) => prev + 1);
				setCelError(null);
				onOpenChange(false);
				onSuccess?.();
			})
			.catch((error: unknown) => {
				const message = getErrorMessage(error);
				// A malformed CEL expression is a field-level problem — show it beneath the CEL
				// editor rather than in a toast (which turns a syntax error into a jarring popup).
				if (conditionMode === "cel" && /cel expression/i.test(message)) {
					setCelError(message);
					return;
				}
				toast.error(message);
			});
	};

	const handleCancel = () => {
		reset();
		setTargets([{ ...DEFAULT_ROUTING_TARGET }]);
		setQuery(defaultQuery);
		setConditionMode("builder");
		setBuilderKey((prev) => prev + 1);
		setCelError(null);
		onOpenChange(false);
	};

	return (
		<Sheet open={open} onOpenChange={onOpenChange}>
			<SheetContent className="flex w-full min-w-1/2 flex-col gap-4 overflow-x-hidden p-0 pt-4">
				<SheetHeader className="flex flex-col items-start px-8 py-4" headerClassName="mb-0 sticky -top-4 bg-card z-10">
					<SheetTitle>{isEditing ? "Edit Routing Rule" : "Create New Routing Rule"}</SheetTitle>
					<SheetDescription>
						{isEditing ? "Update the routing rule configuration" : "Create a new CEL-based routing rule for intelligent request routing"}
					</SheetDescription>
				</SheetHeader>

				<form onSubmit={handleSubmit(onSubmit)} className="flex grow flex-col">
					<div className="flex grow flex-col gap-6 px-8 pb-6">
						{/* Rule Name */}
						<div className="space-y-3">
							<Label htmlFor="name">
								{i18n.t("workspace.routingRules.ruleName")} <span className="text-red-500">*</span>
							</Label>
							<Input
								id="name"
								placeholder={i18n.t("workspace.routingRules.ruleNamePlaceholder")}
								{...register("name", {
									required: "Rule name is required",
									maxLength: 255,
								})}
							/>
							{errors.name && <p className="text-destructive text-sm">{errors.name.message}</p>}
						</div>

						{/* Description */}
						<div className="space-y-3">
							<Label htmlFor="description">{i18n.t("workspace.virtualKeys.description")}</Label>
							<Textarea
								id="description"
								placeholder={i18n.t("workspace.routingRules.descriptionFieldPlaceholder")}
								rows={2}
								{...register("description")}
							/>
						</div>

						{/* Enabled Switch */}
						<div className="flex items-center justify-between rounded-lg border p-4">
							<div className="space-y-0.5">
								<Label htmlFor="enabled">{i18n.t("workspace.routingRules.enableRule")}</Label>
								<p className="text-muted-foreground text-sm">Rule will be active and applied to matching requests</p>
							</div>
							<Switch id="enabled" checked={enabled} onCheckedChange={(checked) => setValue("enabled", checked)} />
						</div>

						{/* Chain Rule Switch */}
						<div className="flex items-center justify-between rounded-lg border p-4">
							<div className="space-y-0.5">
								<Label htmlFor="chain_rule">{i18n.t("workspace.routingRules.chainRule")}</Label>
								<p className="text-muted-foreground text-sm">
									After this rule matches, re-evaluate routing rules using the resolved provider/model as the new context. Useful for
									composing rules, e.g. normalize a model alias first, then route based on the canonical name.
								</p>
							</div>
							<Switch
								id="chain_rule"
								checked={chainRule}
								onCheckedChange={(checked) => setValue("chain_rule", checked)}
								data-testid="routing-rule-chain-rule-switch"
							/>
						</div>

						{/* Scope and Priority - Side by Side */}
						<div className="grid grid-cols-2 gap-4">
							<div className="space-y-3">
								<Label htmlFor="scope">{i18n.t("workspace.routingRules.scope")}</Label>
								<Select
									value={scope}
									onValueChange={(value) => {
										setValue("scope", value as RoutingRuleFormData["scope"]);
										// Clear scope_id when scope changes
										setValue("scope_id", "");
									}}
								>
									<SelectTrigger className="w-full">
										<SelectValue placeholder={i18n.t("workspace.routingRules.selectScope")} />
									</SelectTrigger>
									<SelectContent>
										{ROUTING_RULE_SCOPES.map((scopeOption) => (
											<SelectItem key={scopeOption.value} value={scopeOption.value}>
												{scopeOption.label}
											</SelectItem>
										))}
										{(UserPicker || scope === "user") && <SelectItem value="user">{i18n.t("common.user")}</SelectItem>}
									</SelectContent>
								</Select>
							</div>

							<div className="space-y-3">
								<Label htmlFor="priority">
									{i18n.t("workspace.routingRules.priority")} <span className="text-red-500">*</span>
								</Label>
								<Input
									id="priority"
									type="number"
									min={0}
									max={1000}
									{...register("priority", {
										required: "Priority is required",
										min: { value: 0, message: "Priority must be ≥ 0" },
										max: { value: 1000, message: "Priority must be ≤ 1000" },
										valueAsNumber: true,
									})}
								/>
								<p className="text-muted-foreground text-xs">{i18n.t("workspace.routingRules.priorityDescription")}</p>
								{errors.priority && <p className="text-destructive text-sm">{errors.priority.message}</p>}
							</div>
						</div>

						{scope !== "global" && (
							<div className="space-y-2">
								<Label htmlFor="scope_id">
									{scope === "team" ? "Team" : scope === "customer" ? "Customer" : scope === "user" ? "User" : "Virtual Key"}{" "}
									<span className="text-red-500">*</span>
								</Label>
								{/* A rule stores only its scope_id, so there is no name to seed
								    these with — each selector resolves its own selection. */}
								{scope === "team" && <TeamSelector value={scopeId || ""} onChange={(value) => setValue("scope_id", value)} />}
								{scope === "customer" && <CustomerSelector value={scopeId || ""} onChange={(value) => setValue("scope_id", value)} />}
								{scope === "virtual_key" && <VirtualKeySelector value={scopeId || ""} onChange={(value) => setValue("scope_id", value)} />}
								{scope === "user" &&
									(UserPicker ? (
										<UserPicker value={scopeId || ""} onChange={(value) => setValue("scope_id", value)} />
									) : (
										// No user directory in this build: keep a plain input so
										// existing user-scoped rules remain editable.
										<Input
											id="scope_id"
											data-testid="routing-rule-scope-user-input"
											placeholder="Governance user ID"
											value={scopeId || ""}
											onChange={(e) => setValue("scope_id", e.target.value)}
										/>
									))}
								{/* Teams, customers and virtual keys are all searched lazily inside their
								    selectors, each of which surfaces its own empty state. */}
								{errors.scope_id && <p className="text-destructive text-sm">{errors.scope_id.message}</p>}
							</div>
						)}

						<Separator />

						{/* CEL Rule Builder */}
						<div className="space-y-3">
							<Label>{i18n.t("workspace.routingRules.ruleBuilder")}</Label>
							<p className="text-muted-foreground text-sm">
								Build conditions to determine when this rule should apply. Leave empty to apply this rule to all requests.
							</p>
							<CELRuleBuilder
								key={builderKey}
								initialQuery={query}
								onChange={handleQueryChange}
								providers={availableProviders}
								models={[]}
								allowCustomModels={true}
								allowCelMode={true}
								initialMode={conditionMode}
								initialCel={editingRule?.cel_expression ?? ""}
								onModeChange={handleModeChange}
								celError={celError}
							/>
						</div>

						{/* Note about Token/Request Limits and Budget Configuration */}
						<p className="text-muted-foreground text-xs">
							Note: Ensure token limits, request limits, and budget are configured in{" "}
							<strong>Model Providers → Configurations → {"{provider}"} → Governance</strong> (provider-level) or{" "}
							<strong>Model Providers → Budgets & Limits</strong> section (model-level) before using them in routing rules.
						</p>

						<Separator />

						{/* Routing Targets */}
						<div className="space-y-3">
							<div className="flex items-center justify-between">
								<div>
									<Label>{i18n.t("workspace.routingRules.routingTargets")}</Label>
									<p className="text-muted-foreground mt-0.5 text-xs">{i18n.t("workspace.routingRules.routingTargetsDescription")}</p>
								</div>
								<Button
									type="button"
									variant="outline"
									size="sm"
									onClick={addTarget}
									className="shrink-0 gap-2"
									data-testid="routing-rule-target-add"
								>
									<Plus className="h-4 w-4" />
									{i18n.t("workspace.routingRules.addTarget")}
								</Button>
							</div>

							<div className="space-y-3">
								{targets.map((target, index) => (
									<TargetRow
										key={index}
										target={target}
										index={index}
										providerOptions={providerOptions}
										allKeys={allKeysData}
										showRemove={targets.length > 1}
										onUpdate={updateTarget}
										onRemove={removeTarget}
									/>
								))}
							</div>

							{/* Weight sum indicator */}
							<div
								className={`flex items-center justify-end gap-2 text-xs font-medium ${Math.abs(totalWeight - 1) > 0.001 ? "text-destructive" : "text-muted-foreground"}`}
							>
								{i18n.t("workspace.routingRules.totalWeight")} {totalWeight.toFixed(4)}
								{Math.abs(totalWeight - 1) > 0.001 && <span className="text-destructive">(must equal 1)</span>}
							</div>
						</div>

						{/* Fallbacks */}
						<div className="space-y-3">
							<div className="flex items-center justify-between">
								<div>
									<Label>{i18n.t("workspace.routingRules.fallbacks")}</Label>{" "}
									<p className="text-muted-foreground mt-0.5 text-xs">
										Provider is required, but model is optional. Leave model empty to use the incoming request value.
									</p>
								</div>
								<Button
									type="button"
									variant="outline"
									size="sm"
									onClick={() => setValue("fallbacks", [...(fallbacks || []), ""])}
									className="gap-2"
								>
									<Plus className="h-4 w-4" />
									{i18n.t("workspace.routingRules.addFallback")}
								</Button>
							</div>
							<div className="space-y-2">
								{(fallbacks || []).length === 0 ? (
									<p className="text-muted-foreground text-sm">{i18n.t("workspace.routingRules.noFallbacksConfigured")}</p>
								) : (
									(fallbacks || []).map((fallback, index) => {
										// Parse provider/model from fallback string
										const parts = fallback.split("/");
										const fbProvider = parts[0] || "";
										const fbModel = parts.slice(1).join("/");

										const handleProviderChange = (newProvider: string) => {
											const model = fbModel || "";
											const newFallback = `${newProvider}/${model}`;
											const newFallbacks = [...fallbacks];
											newFallbacks[index] = newFallback;
											setValue("fallbacks", newFallbacks);
										};

										const handleModelChange = (newModel: string) => {
											const prov = fbProvider || "";
											const newFallback = `${prov}/${newModel}`;
											const newFallbacks = [...fallbacks];
											newFallbacks[index] = newFallback;
											setValue("fallbacks", newFallbacks);
										};

										const handleRemove = () => {
											const newFallbacks = fallbacks.filter((_: string, i: number) => i !== index);
											setValue("fallbacks", newFallbacks);
										};

										return (
											<div key={index} className="flex items-center gap-2">
												<div className="flex-1">
													<ComboboxSelect
														options={providerOptions}
														value={fbProvider || null}
														onValueChange={(value) => handleProviderChange(value ?? "")}
														placeholder={i18n.t("workspace.routingRules.selectProvider")}
														className="h-9"
														noPortal
													/>
												</div>
												<div className="flex-1">
													<ModelMultiselect
														provider={fbProvider || undefined}
														value={fbModel}
														onChange={handleModelChange}
														placeholder={i18n.t("workspace.routingRules.incoming")}
														isSingleSelect
														disabled={!fbProvider}
														className="!h-9 !min-h-9 w-full"
													/>
												</div>
												<Button
													type="button"
													variant="ghost"
													size="sm"
													onClick={handleRemove}
													className="h-9 px-2"
													aria-label={`Remove fallback ${index + 1}`}
												>
													<Trash2 className="h-4 w-4" />
												</Button>
											</div>
										);
									})
								)}
							</div>
							<p className="text-muted-foreground text-xs">{i18n.t("workspace.routingRules.fallbacksOrderNote")}</p>
						</div>

						<div className="space-y-3" data-testid="routing-rule-error-fallbacks-section">
							<div className="flex items-center justify-between">
								<div>
									<Label>{i18n.t("workspace.routingRules.errorFallbacks")}</Label>
									<p className="text-muted-foreground mt-0.5 text-xs">{i18n.t("workspace.routingRules.errorFallbacksDescription")}</p>
								</div>
								<Button
									type="button"
									variant="outline"
									size="sm"
									onClick={addErrorFallbackRule}
									className="gap-2"
									data-testid="add-error-fallback-rule-btn"
								>
									<Plus className="h-4 w-4" />
									{i18n.t("workspace.routingRules.addErrorFallbackRule")}
								</Button>
							</div>

							{(errorFallbacks || []).length === 0 ? (
								<p className="text-muted-foreground text-sm">{i18n.t("workspace.routingRules.noErrorFallbacksConfigured")}</p>
							) : (
								<div className="space-y-4">
									{(errorFallbacks || []).map((rule, index) => {
										const isScenarioRule = rule.mode === "scenario";
										const advancedLabel = isScenarioRule
											? i18n.t("workspace.routingRules.errorFallbackSupplementalRecognition")
											: i18n.t("workspace.routingRules.errorFallbackAdvancedConditions");
										const summary = isScenarioRule
											? i18n.t("workspace.routingRules.errorFallbackSimpleDescription")
											: i18n.t("workspace.routingRules.errorFallbackLegacyPreserved");
										const modeTitle = isScenarioRule
											? i18n.t("workspace.routingRules.errorFallbackExpertMode")
											: i18n.t("workspace.routingRules.errorFallbackScenarioMode");
										const modeDescription = isScenarioRule
											? i18n.t("workspace.routingRules.errorFallbackExpertModeDescription")
											: i18n.t("workspace.routingRules.errorFallbackScenarioModeDescription");
										const modeAction = isScenarioRule
											? i18n.t("workspace.routingRules.useExpertMode")
											: i18n.t("workspace.routingRules.useScenarioMode");
										return (
											<div key={index} className="overflow-hidden rounded-lg border" data-testid="error-fallback-rule-card">
												<div className="flex items-center justify-between gap-4 border-b px-4 py-3">
													<span className="text-sm font-medium">
														{i18n.t("workspace.routingRules.errorFallbackRuleNumber", { index: index + 1 })}
													</span>
													<Button
														type="button"
														variant="ghost"
														size="icon"
														onClick={() => removeErrorFallbackRule(index)}
														aria-label={`Remove error fallback rule ${index + 1}`}
													>
														<Trash2 className="h-4 w-4" />
													</Button>
												</div>

												<Collapsible defaultOpen={false}>
													<div className="flex flex-wrap items-end gap-3 p-4 pb-2">
														{rule.mode === "scenario" ? (
															<div className="space-y-2">
																<Label>{i18n.t("workspace.routingRules.errorFallbackScenario")}</Label>
																<Select
																	value={rule.scenario}
																	onValueChange={(value) =>
																		updateErrorFallbackRule(index, (current) => ({
																			...current,
																			mode: "scenario",
																			scenario: value as RoutingErrorFallbackCategory,
																		}))
																	}
																>
																	<SelectTrigger className="h-10 w-[260px]" data-testid="error-fallback-primary-category-select">
																		<ShieldCheck className="mr-2 h-4 w-4" />
																		<SelectValue />
																	</SelectTrigger>
																	<SelectContent>
																		{ERROR_FALLBACK_CATEGORY_OPTIONS.map((category) => (
																			<SelectItem key={category} value={category}>
																				{formatErrorFallbackCategoryLabel(category)}
																			</SelectItem>
																		))}
																	</SelectContent>
																</Select>
															</div>
														) : (
															<div className="space-y-1">
																<Label>{i18n.t("workspace.routingRules.errorFallbackLegacyRule")}</Label>
																<p className="text-muted-foreground text-xs">
																	{i18n.t("workspace.routingRules.errorFallbackLegacyRuleDescription")}
																</p>
															</div>
														)}
														<CollapsibleTrigger asChild>
															<Button
																type="button"
																variant="outline"
																size="sm"
																className="group ml-auto gap-2"
																data-testid="error-fallback-advanced-trigger"
															>
																<SlidersHorizontal className="h-4 w-4" />
																{advancedLabel}
																<ChevronDown className="h-4 w-4 transition-transform group-data-[state=open]:rotate-180" />
															</Button>
														</CollapsibleTrigger>
													</div>
													<p className="text-muted-foreground px-4 pb-4 text-xs">{summary}</p>

													<CollapsibleContent className="bg-muted/20 border-t p-4" data-testid="error-fallback-advanced-content">
														<div className="mb-4 flex flex-wrap items-center justify-between gap-3 rounded-md border border-dashed px-3 py-2.5">
															<div className="space-y-0.5">
																<p className="text-sm font-medium">{modeTitle}</p>
																<p className="text-muted-foreground text-xs">{modeDescription}</p>
															</div>
															<Button
																type="button"
																variant="outline"
																size="sm"
																onClick={() =>
																	updateErrorFallbackRule(index, (current) =>
																		switchErrorFallbackMode(current, current.mode === "scenario" ? "legacy" : "scenario"),
																	)
																}
																data-testid="error-fallback-mode-toggle"
															>
																{modeAction}
															</Button>
														</div>
														<div className="mb-4 max-w-sm space-y-2">
															<Label>{i18n.t("workspace.routingRules.errorFallbackRuleName")}</Label>
															<Input
																value={rule.name || ""}
																onChange={(event) =>
																	updateErrorFallbackRule(index, (current) => ({ ...current, name: event.target.value }))
																}
																placeholder={i18n.t("workspace.routingRules.errorFallbackRuleNamePlaceholder")}
																data-testid="error-fallback-rule-name-input"
															/>
														</div>
														{rule.mode === "scenario" ? (
															<div className="space-y-4">
																<div className="text-muted-foreground rounded-md border border-dashed px-3 py-2 text-xs">
																	{i18n.t("workspace.routingRules.errorFallbackSupplementDescription")}
																</div>
																<div className="grid gap-3 md:grid-cols-2">
																	<div className="space-y-2">
																		<Label>{i18n.t("workspace.routingRules.errorFallbackProviders")}</Label>
																		<Input
																			value={toCommaSeparated(rule.supplement.providers)}
																			onChange={(event) =>
																				updateErrorFallbackRule(index, (current) => ({
																					...current,
																					mode: "scenario",
																					supplement: {
																						...current.supplement,
																						providers: parseCommaSeparatedList(event.target.value),
																					},
																				}))
																			}
																			placeholder={i18n.t("workspace.routingRules.errorFallbackProvidersPlaceholder")}
																		/>
																	</div>
																	<div className="space-y-2">
																		<Label>{i18n.t("workspace.routingRules.errorFallbackErrorTypes")}</Label>
																		<Input
																			value={toCommaSeparated(rule.supplement.error_types)}
																			onChange={(event) =>
																				updateErrorFallbackRule(index, (current) => ({
																					...current,
																					mode: "scenario",
																					supplement: {
																						...current.supplement,
																						error_types: parseCommaSeparatedList(event.target.value),
																					},
																				}))
																			}
																			placeholder={i18n.t("workspace.routingRules.errorFallbackCommaSeparatedPlaceholder")}
																		/>
																	</div>
																	<div className="space-y-2">
																		<Label>{i18n.t("workspace.routingRules.errorFallbackErrorCodes")}</Label>
																		<Input
																			value={toCommaSeparated(rule.supplement.error_codes)}
																			onChange={(event) =>
																				updateErrorFallbackRule(index, (current) => ({
																					...current,
																					mode: "scenario",
																					supplement: {
																						...current.supplement,
																						error_codes: parseCommaSeparatedList(event.target.value),
																					},
																				}))
																			}
																			placeholder={i18n.t("workspace.routingRules.errorFallbackCommaSeparatedPlaceholder")}
																		/>
																	</div>
																	<div className="space-y-2">
																		<Label>{i18n.t("workspace.routingRules.errorFallbackStatusCodes")}</Label>
																		<Input
																			value={toCommaSeparated(rule.supplement.status_codes)}
																			onChange={(event) =>
																				updateErrorFallbackRule(index, (current) => ({
																					...current,
																					mode: "scenario",
																					supplement: {
																						...current.supplement,
																						status_codes: parseCommaSeparatedNumbers(event.target.value),
																					},
																				}))
																			}
																			placeholder="400, 429, 503"
																		/>
																	</div>
																	<div className="space-y-2 md:col-span-2">
																		<Label>{i18n.t("workspace.routingRules.errorFallbackMessageContainsAny")}</Label>
																		<Input
																			value={toCommaSeparated(rule.supplement.message_contains_any)}
																			onChange={(event) =>
																				updateErrorFallbackRule(index, (current) => ({
																					...current,
																					mode: "scenario",
																					supplement: {
																						...current.supplement,
																						message_contains_any: parseCommaSeparatedList(event.target.value),
																					},
																				}))
																			}
																			placeholder={i18n.t("workspace.routingRules.errorFallbackMessageContainsAnyPlaceholder")}
																		/>
																	</div>
																</div>
															</div>
														) : (
															<>
																<div className="rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-700 dark:text-amber-300">
																	{i18n.t("workspace.routingRules.errorFallbackLegacyWarning")}
																</div>
																<div className="mb-4 space-y-2">
																	<Label>{i18n.t("workspace.routingRules.errorFallbackAdditionalCategories")}</Label>
																	<div className="flex flex-wrap gap-2">
																		{ERROR_FALLBACK_CATEGORY_OPTIONS.map((category) => {
																			const active = rule.when.categories.includes(category);
																			return (
																				<Button
																					key={category}
																					type="button"
																					variant={active ? "default" : "outline"}
																					size="sm"
																					onClick={() =>
																						updateErrorFallbackRule(index, (current) => ({
																							...current,
																							when: {
																								...current.when,
																								categories: active
																									? current.when.categories.filter((item) => item !== category)
																									: [...current.when.categories, category],
																							},
																						}))
																					}
																					data-testid={`error-fallback-category-${category}-btn`}
																				>
																					{formatErrorFallbackCategoryLabel(category)}
																				</Button>
																			);
																		})}
																	</div>
																</div>

																<div className="grid gap-3 md:grid-cols-2">
																	<div className="space-y-2">
																		<Label>{i18n.t("workspace.routingRules.errorFallbackErrorTypes")}</Label>
																		<Input
																			value={toCommaSeparated(rule.when.error_types)}
																			onChange={(event) =>
																				updateErrorFallbackRule(index, (current) => ({
																					...current,
																					when: {
																						...current.when,
																						error_types: parseCommaSeparatedList(event.target.value),
																					},
																				}))
																			}
																			placeholder={i18n.t("workspace.routingRules.errorFallbackCommaSeparatedPlaceholder")}
																		/>
																	</div>
																	<div className="space-y-2">
																		<Label>{i18n.t("workspace.routingRules.errorFallbackErrorCodes")}</Label>
																		<Input
																			value={toCommaSeparated(rule.when.error_codes)}
																			onChange={(event) =>
																				updateErrorFallbackRule(index, (current) => ({
																					...current,
																					when: {
																						...current.when,
																						error_codes: parseCommaSeparatedList(event.target.value),
																					},
																				}))
																			}
																			placeholder={i18n.t("workspace.routingRules.errorFallbackCommaSeparatedPlaceholder")}
																		/>
																	</div>
																	<div className="space-y-2">
																		<Label>{i18n.t("workspace.routingRules.errorFallbackStatusCodes")}</Label>
																		<Input
																			value={toCommaSeparated(rule.when.status_codes)}
																			onChange={(event) =>
																				updateErrorFallbackRule(index, (current) => ({
																					...current,
																					when: {
																						...current.when,
																						status_codes: parseCommaSeparatedNumbers(event.target.value),
																					},
																				}))
																			}
																			placeholder="400, 429, 503"
																		/>
																	</div>
																	<div className="space-y-2">
																		<Label>{i18n.t("workspace.routingRules.errorFallbackMessageContains")}</Label>
																		<Input
																			value={toCommaSeparated(rule.when.message_contains)}
																			onChange={(event) =>
																				updateErrorFallbackRule(index, (current) => ({
																					...current,
																					when: {
																						...current.when,
																						message_contains: parseCommaSeparatedList(event.target.value),
																					},
																				}))
																			}
																			placeholder={i18n.t("workspace.routingRules.errorFallbackMessageContainsPlaceholder")}
																		/>
																	</div>
																</div>
															</>
														)}
													</CollapsibleContent>
												</Collapsible>

												<div className="space-y-3 p-4 pt-0">
													<div className="flex items-center justify-between">
														<div>
															<Label className="text-base">
																{i18n.t("workspace.routingRules.errorFallbackChain")}
																<span className="text-muted-foreground ml-2 font-normal">
																	{i18n.t("workspace.routingRules.errorFallbackOrderedHint")}
																</span>
															</Label>
															<p className="text-muted-foreground mt-0.5 text-xs">
																{i18n.t("workspace.routingRules.errorFallbackChainDescription")}
															</p>
														</div>
														<Button
															type="button"
															variant="outline"
															size="sm"
															onClick={() =>
																updateErrorFallbackRule(index, (current) => ({
																	...current,
																	fallbacks: [...current.fallbacks, ""],
																}))
															}
															className="gap-2"
															data-testid="add-error-fallback-target-btn"
														>
															<Plus className="h-4 w-4" />
															{i18n.t("workspace.routingRules.addErrorFallbackTarget")}
														</Button>
													</div>

													<div
														className="text-muted-foreground flex items-start gap-2 rounded-md border px-3 py-2.5 text-xs"
														data-testid="error-fallback-exhaustion-note"
													>
														<AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-500" />
														<span>{i18n.t("workspace.routingRules.errorFallbackExhaustionNote")}</span>
													</div>

													{rule.fallbacks.length === 0 ? (
														<p className="text-muted-foreground text-sm">{i18n.t("workspace.routingRules.noFallbacksConfigured")}</p>
													) : (
														<div className="space-y-2">
															{rule.fallbacks.map((fallback, fallbackIndex) => {
																const parts = fallback.split("/");
																const fbProvider = parts[0] || "";
																const fbModel = parts.slice(1).join("/");

																return (
																	<div
																		key={fallbackIndex}
																		className="flex items-stretch gap-2"
																		draggable
																		onDragStart={(event) => event.dataTransfer.setData("text/plain", String(fallbackIndex))}
																		onDragOver={(event) => event.preventDefault()}
																		onDrop={(event) => {
																			event.preventDefault();
																			const fromIndex = Number.parseInt(event.dataTransfer.getData("text/plain"), 10);
																			if (Number.isFinite(fromIndex)) moveErrorFallbackTarget(index, fromIndex, fallbackIndex);
																		}}
																		data-testid={`error-fallback-target-${fallbackIndex}`}
																	>
																		<div className="text-muted-foreground flex w-12 shrink-0 cursor-grab items-center justify-between active:cursor-grabbing">
																			<GripVertical className="h-4 w-4" aria-hidden="true" />
																			<span className="border-primary text-primary flex h-8 w-8 items-center justify-center rounded-full border text-sm font-semibold">
																				{fallbackIndex + 1}
																			</span>
																		</div>
																		<div className="flex flex-1 items-center gap-2 rounded-md border p-2">
																			<div className="flex-1">
																				<ComboboxSelect
																					options={providerOptions}
																					value={fbProvider || null}
																					onValueChange={(value) =>
																						updateErrorFallbackRule(index, (current) => {
																							const nextFallbacks = [...current.fallbacks];
																							nextFallbacks[fallbackIndex] = `${value ?? ""}/${fbModel}`;
																							return {
																								...current,
																								fallbacks: nextFallbacks,
																							};
																						})
																					}
																					placeholder={i18n.t("workspace.routingRules.selectProvider")}
																					className="h-9"
																					noPortal
																				/>
																			</div>
																			<div className="flex-1">
																				<ModelMultiselect
																					provider={fbProvider || undefined}
																					value={fbModel}
																					onChange={(value) =>
																						updateErrorFallbackRule(index, (current) => {
																							const nextFallbacks = [...current.fallbacks];
																							nextFallbacks[fallbackIndex] = `${fbProvider}/${value}`;
																							return {
																								...current,
																								fallbacks: nextFallbacks,
																							};
																						})
																					}
																					placeholder={i18n.t("workspace.routingRules.incoming")}
																					isSingleSelect
																					disabled={!fbProvider}
																					className="!h-9 !min-h-9 w-full"
																				/>
																			</div>
																			<Button
																				type="button"
																				variant="ghost"
																				size="sm"
																				onClick={() =>
																					updateErrorFallbackRule(index, (current) => ({
																						...current,
																						fallbacks: current.fallbacks.filter((_, currentIndex) => currentIndex !== fallbackIndex),
																					}))
																				}
																				className="h-9 px-2"
																			>
																				<Trash2 className="h-4 w-4" />
																			</Button>
																		</div>
																	</div>
																);
															})}
														</div>
													)}
												</div>
											</div>
										);
									})}
								</div>
							)}
						</div>
					</div>
					{/* Action Buttons */}
					<div className="bg-card sticky bottom-0 flex justify-end gap-3 border-t px-8 py-4">
						<Button type="button" variant="outline" onClick={handleCancel} disabled={isLoading}>
							{i18n.t("common.cancel")}
						</Button>
						<Button type="submit" disabled={isLoading || !hasRequiredAccess}>
							{isEditing ? "Update Rule" : "Save Rule"}
						</Button>
					</div>
				</form>
			</SheetContent>
		</Sheet>
	);
}

interface TargetRowProps {
	target: RoutingTargetFormData;
	index: number;
	providerOptions: Array<{
		label: string;
		value: string;
		icon: React.ReactNode;
	}>;
	allKeys: Array<{ key_id: string; name: string; provider: string }>;
	showRemove: boolean;
	onUpdate: (index: number, field: keyof RoutingTargetFormData, value: string | number) => void;
	onRemove: (index: number) => void;
}

function TargetRow({ target, index, providerOptions, allKeys, showRemove, onUpdate, onRemove }: TargetRowProps) {
	const availableKeys = target.provider
		? allKeys.filter((k) => k.provider === target.provider).map((k) => ({ id: k.key_id, name: k.name }))
		: [];

	return (
		<div className="space-y-3 rounded-lg border p-3" data-testid={`routing-target-${index}`}>
			<div className="flex items-center justify-between">
				<span className="text-muted-foreground text-sm font-medium">Target {index + 1}</span>
				<div className="flex items-center gap-2">
					<div className="flex items-center gap-1.5">
						<Label htmlFor={`routing-target-${index}-weight-input`} className="text-muted-foreground shrink-0 text-xs">
							{i18n.t("workspace.virtualKeys.weight")}
						</Label>
						<Input
							id={`routing-target-${index}-weight-input`}
							type="number"
							min={0.001}
							max={1}
							step={0.001}
							value={target.weight}
							onChange={(e) => onUpdate(index, "weight", parseFloat(e.target.value) || 0)}
							className="h-8 w-24 text-sm"
							data-testid={`routing-target-${index}-weight-input`}
						/>
					</div>
					{showRemove && (
						<Button
							type="button"
							variant="ghost"
							size="sm"
							onClick={() => onRemove(index)}
							className="h-8 w-8 p-0"
							aria-label={`Remove target ${index + 1}`}
							data-testid={`routing-target-${index}-remove-button`}
						>
							<Trash2 className="h-3.5 w-3.5" />
						</Button>
					)}
				</div>
			</div>

			<div className="grid grid-cols-2 gap-3">
				<div className="space-y-1.5">
					<Label id={`routing-target-${index}-provider-label`} className="text-xs">
						{i18n.t("workspace.logs.colProvider")}
					</Label>
					<div className="flex gap-1.5">
						<ComboboxSelect
							options={providerOptions}
							value={target.provider || null}
							onValueChange={(value) => {
								onUpdate(index, "provider", value ?? "");
								onUpdate(index, "model", "");
								onUpdate(index, "key_id", "");
							}}
							placeholder={i18n.t("workspace.routingRules.incoming")}
							className="h-9 flex-1 text-sm"
							data-testid={`routing-target-${index}-provider-select`}
							noPortal
						/>
						{target.provider && (
							<Button
								type="button"
								variant="outline"
								size="sm"
								onClick={() => {
									onUpdate(index, "provider", "");
									onUpdate(index, "model", "");
									onUpdate(index, "key_id", "");
								}}
								className="h-9 w-9 p-0"
								aria-label={`Clear provider for target ${index + 1}`}
								data-testid={`routing-target-${index}-provider-clear`}
							>
								<X className="h-3.5 w-3.5" />
							</Button>
						)}
					</div>
				</div>

				<div className="space-y-1.5">
					<Label id={`routing-target-${index}-model-label`} className="text-xs">
						{i18n.t("workspace.logs.colModel")}
					</Label>
					<div className="flex gap-1.5">
						<div className="flex-1" data-testid={`routing-target-${index}-model-select`}>
							<ModelMultiselect
								provider={target.provider || undefined}
								value={target.model}
								onChange={(value) => onUpdate(index, "model", value)}
								placeholder={i18n.t("workspace.routingRules.incoming")}
								isSingleSelect
								loadModelsOnEmptyProvider
								className="!h-9 !min-h-9"
								inputId={`routing-target-${index}-model-input`}
								ariaLabelledBy={`routing-target-${index}-model-label`}
							/>
						</div>
						{target.model && (
							<Button
								type="button"
								variant="outline"
								size="sm"
								onClick={() => onUpdate(index, "model", "")}
								className="h-9 w-9 p-0"
								aria-label={`Clear model for target ${index + 1}`}
								data-testid={`routing-target-${index}-model-clear`}
							>
								<X className="h-3.5 w-3.5" />
							</Button>
						)}
					</div>
				</div>
			</div>

			{target.provider && (availableKeys.length > 0 || target.key_id) && (
				<div className="space-y-1.5">
					<Label id={`routing-target-${index}-apikey-label`} className="text-xs">
						{i18n.t("workspace.virtualKeys.apiKey")}{" "}
						<span className="text-muted-foreground">(optional; leave unset for load-balanced selection)</span>
					</Label>
					<div className="flex gap-1.5">
						<Select value={target.key_id || ""} onValueChange={(value) => onUpdate(index, "key_id", value)}>
							<SelectTrigger
								id={`routing-target-${index}-apikey-select`}
								aria-labelledby={`routing-target-${index}-apikey-label`}
								className="h-9 flex-1 text-sm"
								data-testid={`routing-target-${index}-apikey-select`}
							>
								<SelectValue placeholder={i18n.t("workspace.routingRules.selectKey")} />
							</SelectTrigger>
							<SelectContent>
								{availableKeys.map((key) => (
									<SelectItem key={key.id} value={key.id}>
										{key.name}
									</SelectItem>
								))}
								{target.key_id && !availableKeys.some((k) => k.id === target.key_id) && (
									<SelectItem key={`pinned-${target.key_id}`} value={target.key_id}>
										(pinned) {target.key_id}
									</SelectItem>
								)}
							</SelectContent>
						</Select>
						{target.key_id && (
							<Button
								type="button"
								variant="outline"
								size="sm"
								onClick={() => onUpdate(index, "key_id", "")}
								className="h-9 w-9 p-0"
								aria-label={`Clear API key for target ${index + 1}`}
								data-testid={`routing-target-${index}-apikey-clear`}
							>
								<X className="h-3.5 w-3.5" />
							</Button>
						)}
					</div>
				</div>
			)}
		</div>
	);
}