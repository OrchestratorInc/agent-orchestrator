export function pageHidden(): boolean {
	return typeof document !== "undefined" && document.visibilityState === "hidden";
}
