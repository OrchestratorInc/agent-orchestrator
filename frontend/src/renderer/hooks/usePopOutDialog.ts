import { useEffect, useRef, type KeyboardEvent } from "react";

/**
 * Dialog semantics for a maximized pop-out overlay: focus moves into the
 * overlay on open and returns to the opener on close, and Escape un-maximizes
 * unless a nested menu or popup already consumed it.
 */
export function usePopOutDialog<T extends HTMLElement>(onClose?: () => void) {
	const ref = useRef<T>(null);
	useEffect(() => {
		const opener = document.activeElement as HTMLElement | null;
		ref.current?.focus({ preventScroll: true });
		return () => {
			if (opener?.isConnected && opener !== document.body) opener.focus({ preventScroll: true });
		};
	}, []);
	return {
		ref,
		role: "dialog" as const,
		"aria-modal": true as const,
		tabIndex: -1,
		onKeyDown: (event: KeyboardEvent) => {
			if (event.key !== "Escape" || event.defaultPrevented || event.nativeEvent.isComposing || !onClose) return;
			event.stopPropagation();
			onClose();
		},
	};
}
