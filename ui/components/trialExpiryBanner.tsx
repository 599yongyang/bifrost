import { TRIAL_EXPIRY } from "@/lib/constants/config";
import { cn } from "@/lib/utils";
import { differenceInDays } from "date-fns";
import { AlertTriangle } from "lucide-react";
import i18n from "@/lib/i18n";

export default function TrialExpiryBanner() {
	if (!TRIAL_EXPIRY) return null;

	const daysRemaining = differenceInDays(TRIAL_EXPIRY, new Date());
	const expired = daysRemaining < 0;
	if (!expired && daysRemaining > 7) return null;
	const critical = !expired && daysRemaining <= 3;

	const subject = expired ? "I need help with my expired enterprise trial" : "I need help extending my enterprise trial";
	const supportHref = `mailto:contact@getmaxim.ai?subject=${encodeURIComponent(subject)}`;

	return (
		<div
			id="trial-notification-banner"
			className={cn(
				"sticky top-0 z-10 flex w-full items-center justify-center gap-2 rounded-tl-md rounded-tr-md px-4 py-2 text-xs font-medium",
				expired || critical ? "bg-[#ffebea] text-red-700 dark:text-red-400" : "bg-[#fff4e4] text-amber-700 dark:text-amber-400",
			)}
			role="status"
		>
			<AlertTriangle className="h-3.5 w-3.5" strokeWidth={2} />
			{expired ? (
				<span>
					{i18n.t("supplemental.trialExpired")}{" "}
					<a href={supportHref} className="font-semibold underline underline-offset-2">
						{i18n.t("supplemental.contactUs")}
					</a>{" "}
					{i18n.t("supplemental.assistanceSuffix")}
				</span>
			) : (
				<span>
					{i18n.t("supplemental.trialExpiresIn")} {daysRemaining} {daysRemaining === 1 ? i18n.t("common.day") : i18n.t("common.days")}.{" "}
					<a href={supportHref} className="font-semibold underline underline-offset-2">
						{i18n.t("supplemental.contactUs")}
					</a>{" "}
					{i18n.t("supplemental.assistanceSuffix")}
				</span>
			)}
		</div>
	);
}
