import { useEffect, useRef } from "react";

export function useSuppressStrayFocusRing(open: boolean) {
	const closedByPointerRef = useRef(false);
	useEffect(() => {
		if (!open) return;
		closedByPointerRef.current = false;
		const markPointer = () => {
			closedByPointerRef.current = true;
		};
		document.addEventListener("pointerdown", markPointer, true);
		return () => document.removeEventListener("pointerdown", markPointer, true);
	}, [open]);

	return (event: Event) => {
		if (closedByPointerRef.current) event.preventDefault();
	};
}
