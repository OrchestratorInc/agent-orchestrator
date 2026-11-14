import { useEffect, useRef, type KeyboardEvent } from "react";

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
