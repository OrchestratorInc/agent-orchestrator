/**
 * True while the window draws no frames: minimized, on another Space, or fully
 * covered (Electron reports an occluded window as hidden). Animations and native
 * smooth scrolls started now do not play until the window is seen again, so
 * work done while hidden must land in its final state at once.
 */
export function pageHidden(): boolean {
	return typeof document !== "undefined" && document.visibilityState === "hidden";
}

/**
 * True while `element` is not rendered: it or an ancestor is `display: none`
 * (or skips its contents). The shell hides the page that way under Settings
 * while it stays mounted, so its effects and observers keep running and read
 * every size as 0. Acting on those zeros replaces the real geometry, and the
 * page comes back laid out against them for a frame before it moves back.
 * Leave the last measurements alone until the element is shown again.
 * Environments without `checkVisibility` (jsdom) count as rendered.
 */
export function notRendered(element: Element): boolean {
	return typeof element.checkVisibility === "function" && !element.checkVisibility();
}
