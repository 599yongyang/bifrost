import i18n from "@/lib/i18n";
import { Link } from "@tanstack/react-router";

export function NotFoundComponent() {
	return (
		<main className="h-base flex items-center justify-center p-6">
			<div className="mx-auto w-full max-w-md text-center">
				<p className="text-foreground text-7xl font-bold tracking-tight">404</p>
				<h1 className="text-foreground mt-4 text-2xl font-semibold">{i18n.t("systemCopy.__notFound_page_not_found")}</h1>
				<p className="text-muted-foreground mt-2 text-sm">
					{i18n.t("systemCopy.__notFound_the_page_you_are_looking_for_doesn_t_exist_or_has_been_m")}
				</p>
				<div className="mt-6 flex items-center justify-center gap-3">
					<Link
						data-testid="not-found-go-home-link"
						to="/workspace/logs"
						className="bg-primary text-primary-foreground focus-visible:ring-primary inline-flex items-center rounded-sm px-4 py-2 text-sm font-medium transition-opacity hover:opacity-90 focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:outline-none"
					>
						{i18n.t("systemCopy.__notFound_go_home")}
					</Link>
				</div>
			</div>
		</main>
	);
}