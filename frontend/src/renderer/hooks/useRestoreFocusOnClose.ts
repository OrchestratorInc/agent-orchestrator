import { useCallback, useLayoutEffect, useRef } from "react";

const COMPOSER_SELECTOR = '[aria-label="Message the agent"]';

/**
 * Radix aims its close-time focus at a DialogTrigger, but programmatically
 * opened dialogs have none, so keyboard users get dropped on <body>. Remember
 * whatever had focus when the dialog opened and put it back on close; when that
 * element is gone (a confirmed action removed its trigger), fall back to the
 * chat composer if present. Pass the result to `onCloseAutoFocus`.
 */
export function useRestoreFocusOnClose(open: boolean) {
	const returnFocusRef = useRef<HTMLElement | null>(null);
	useLayoutEffect(() => {
		if (open) returnFocusRef.current = document.activeElement as HTMLElement | null;
	}, [open]);

	return useCallback((event: Event) => {
		const remembered = returnFocusRef.current;
		const target = remembered?.isConnected && remembered !== document.body
			? remembered
			: document.querySelector<HTMLElement>(COMPOSER_SELECTOR);
		if (!target) return;
		event.preventDefault();
		target.focus();
	}, []);
}
