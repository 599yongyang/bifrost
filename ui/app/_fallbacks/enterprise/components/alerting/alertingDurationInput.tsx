import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import i18n from "@/lib/i18n";
import type { ReactNode } from "react";
import { maxAlertWindowValue, type AlertWindowUnit } from "./alertingDuration";

type AlertingDurationInputProps = {
	label?: string;
	value: string;
	unit: AlertWindowUnit;
	onChange: (value: string, unit: AlertWindowUnit) => void;
	allowZero?: boolean;
	testIdPrefix: string;
	description?: ReactNode;
};

const unitLabel = (unit: AlertWindowUnit) => i18n.t(`workspace.alerting.${unit}`);

export function AlertingDurationInput({
	label,
	value,
	unit,
	onChange,
	allowZero = false,
	testIdPrefix,
	description,
}: AlertingDurationInputProps) {
	return (
		<div className="grid min-w-0 gap-2">
			{label && <Label>{label}</Label>}
			<div className="flex min-w-0">
				<Input
					data-testid={`${testIdPrefix}-value`}
					min={allowZero ? "0" : "1"}
					max={maxAlertWindowValue[unit]}
					step="1"
					type="number"
					required
					className="rounded-r-none"
					value={value}
					onChange={(event) => onChange(event.target.value, unit)}
				/>
				<Select value={unit} onValueChange={(nextUnit) => onChange(value, nextUnit as AlertWindowUnit)}>
					<SelectTrigger className="w-28 shrink-0 rounded-l-none border-l-0" data-testid={`${testIdPrefix}-unit`}>
						<SelectValue />
					</SelectTrigger>
					<SelectContent>
						<SelectItem value="minutes">{unitLabel("minutes")}</SelectItem>
						<SelectItem value="hours">{unitLabel("hours")}</SelectItem>
						<SelectItem value="days">{unitLabel("days")}</SelectItem>
					</SelectContent>
				</Select>
			</div>
			{description && <p className="text-muted-foreground text-xs">{description}</p>}
		</div>
	);
}