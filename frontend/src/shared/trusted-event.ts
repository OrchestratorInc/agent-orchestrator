/**
 * Whether the browser itself produced `event` from real user input. A page's
 * scripts share the DOM the annotation overlay lives in and can call `click()`
 * on its controls, but cannot forge a trusted event.
 */
export function isTrustedUserEvent(event: Event): boolean {
	return event.isTrusted;
}
