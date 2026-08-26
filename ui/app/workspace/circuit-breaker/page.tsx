import { CircuitBreakerView } from "./views/circuitBreakerView";

export default function CircuitBreakerPage() {
	return (
		<div className="no-padding-parent mx-auto flex h-[calc(100dvh-1rem)] min-h-0 w-full max-w-7xl flex-col overflow-hidden p-4">
			<CircuitBreakerView />
		</div>
	);
}