import { useCallback, useLayoutEffect, useRef } from "react";

const COMPOSER_SELECTOR = '[aria-label="Message the agent"]';

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
