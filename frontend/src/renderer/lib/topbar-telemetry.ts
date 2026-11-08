import { useUiStore } from "../stores/ui-store";
import { isSimplifiedTopbar } from "./simplified-topbar";
import { captureRendererEvent } from "./telemetry";
import type { TopbarAction, TopbarView } from "./topbar-actions";

/**
 * Counts a click on a top bar control, to decide what stays in the simplified
 * bar. Only that bar reports: the previous one stays silent.
 */
export function captureTopbarAction(action: TopbarAction, view?: TopbarView): void {
	if (!isSimplifiedTopbar(useUiStore.getState())) return;
	void captureRendererEvent("ao.renderer.topbar_action_clicked", view ? { action, view } : { action });
}
