/**
 * Marks which edges of a scroll window still have content past them, so the
 * `.edge-fade-viewport` mask fades only those edges. A window that fits shows no
 * fade; one scrolled to its end fades only the top.
 */
export function syncEdgeFade(viewport: HTMLElement): void {
	viewport.toggleAttribute("data-fade-top", viewport.scrollTop > 1);
	viewport.toggleAttribute(
		"data-fade-bottom",
		viewport.scrollTop + viewport.clientHeight < viewport.scrollHeight - 1,
	);
}
