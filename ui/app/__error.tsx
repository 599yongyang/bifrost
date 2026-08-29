import i18n from "@/lib/i18n";
import { Button } from "@/components/ui/button";
import { isSkewError, reportSkew } from "@/lib/utils/versionSkew";
import type { ErrorComponentProps } from "@tanstack/react-router";
import { useEffect } from "react";
import { UpdatingScreen } from "./__updating";

export function ErrorComponent({ error }: Partial<ErrorComponentProps>) {
	const skew = isSkewError(error);

	// Notifying skew subscribers is a store write, so keep it out of render.
	useEffect(() => {
		if (skew) reportSkew("hard");
	}, [skew]);

	if (skew) {
		return <UpdatingScreen />;
	}

	return (
		<main className="h-base flex items-center justify-center p-6">
			<div className="mx-auto w-full max-w-md text-center">
				<p className="text-foreground text-7xl font-bold tracking-tight">500</p>
				<h1 className="text-foreground mt-4 text-2xl font-semibold">{i18n.t("systemCopy.__error_something_went_wrong")}</h1>
				<p className="text-muted-foreground mt-2 text-sm">{i18n.t("systemCopy.__error_something_went_wrong_please_refresh_the_page")}</p>
				<div className="mt-6 flex items-center justify-center gap-3">
					<Button size={"sm"} data-testid="error-reload-btn" onClick={() => window.location.reload()}>
						{i18n.t("systemCopy.__error_reload")}
					</Button>
				</div>
			</div>
		</main>
	);
}