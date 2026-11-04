import { captureRendererEvent } from "./telemetry";
import type { TopbarAction, TopbarView } from "./topbar-actions";

export function captureTopbarAction(action: TopbarAction, view?: TopbarView): void {
	void captureRendererEvent("ao.renderer.topbar_action_clicked", view ? { action, view } : { action });
}
