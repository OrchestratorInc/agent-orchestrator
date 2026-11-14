import type { UpdateOutcome } from "../../shared/update-telemetry";
import { aoBridge } from "./bridge";
import { captureRendererEvent, clearCheckUpdateFailures, telemetryStorage } from "./telemetry";

export function startUpdateTelemetry(): () => void {
	const off = aoBridge.updates.onTelemetry?.((outcome: UpdateOutcome) => {
		if (outcome.event === "ao.renderer.update_check_succeeded") {
			const storage = telemetryStorage();
			if (storage) clearCheckUpdateFailures(storage);
			return;
		}
		void captureRendererEvent(outcome.event, {
			phase: outcome.phase,
			trigger: outcome.trigger,
			error_category: outcome.error_category,
			to_version: outcome.to_version,
			differential_eligible: outcome.differential_eligible,
			transfer_mode: outcome.transfer_mode,
			fallback: outcome.fallback,
			transferred_bytes: outcome.transferred_bytes,
			target_bytes: outcome.target_bytes,
		});
	});
	return () => off?.();
}
