import { Button } from "@/components/ui/button";
import { Route } from "lucide-react";
import { ArrowUpRight } from "lucide-react";
import { routingRulesCopy as copy } from "../routingRulesCopy";

const ROUTING_RULES_DOCS_URL = "https://docs.getbifrost.ai/providers/routing-rules";

interface RoutingRulesEmptyStateProps {
	onAddClick: () => void;
	canCreate?: boolean;
}

export function RoutingRulesEmptyState({ onAddClick, canCreate = true }: RoutingRulesEmptyStateProps) {
	return (
		<div
			className="flex min-h-[80vh] w-full flex-col items-center justify-center gap-4 py-16 text-center"
			data-testid="routing-rules-empty-state"
		>
			<div className="text-muted-foreground">
				<Route className="h-[5.5rem] w-[5.5rem]" strokeWidth={1} />
			</div>
			<div className="flex flex-col gap-1">
				<h1 className="text-muted-foreground text-xl font-medium">{copy.emptyTitle}</h1>
				<div className="text-muted-foreground mx-auto mt-2 w-full max-w-[600px] text-sm font-normal">
					{copy.emptyDescription}
				</div>
				<div className="mx-auto mt-6 flex flex-row flex-wrap items-center justify-center gap-2">
					<Button
						variant="outline"
						aria-label={copy.readMoreAria}
						data-testid="routing-rules-empty-read-more"
						onClick={() => {
							window.open(`${ROUTING_RULES_DOCS_URL}?utm_source=bfd`, "_blank", "noopener,noreferrer");
						}}
					>
						{copy.readMore} <ArrowUpRight className="text-muted-foreground h-3 w-3" />
					</Button>
					<Button
						aria-label={copy.createFirstRule}
						data-testid="create-routing-rule-btn"
						onClick={onAddClick}
						disabled={!canCreate}
					>
						{copy.newRule}
					</Button>
				</div>
			</div>
		</div>
	);
}
