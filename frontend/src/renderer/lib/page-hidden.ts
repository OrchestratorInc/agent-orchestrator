/**
 * True while the window draws no frames: minimized, on another Space, or fully
 * covered (Electron reports an occluded window as hidden). Animations and native
 * smooth scrolls started now do not play until the window is seen again, so
 * work done while hidden must land in its final state at once.
 */
export function pageHidden(): boolean {
	return typeof document !== "undefined" && document.visibilityState === "hidden";
}
