import { useEffect, useRef, useState, type RefObject } from "react";
import { codeSelectionRange } from "./useCodeSelection";

export function hasCodeSelection(container: HTMLElement | null): boolean {
	return Boolean(
		container &&
			[...container.querySelectorAll<HTMLElement>("diffs-container")].some(
				(host) => codeSelectionRange(host),
			),
	);
}

export function useSelectionSnapshot<T>(
	container: RefObject<HTMLDivElement | null>,
	value: T,
	identity: string,
): T {
	const snapshot = useRef({ value, identity });
	const [selected, setSelected] = useState(false);
	useEffect(() => {
		const update = () => setSelected(hasCodeSelection(container.current));
		update();
		document.addEventListener("selectionchange", update);
		document.addEventListener("pointerup", update);
		return () => {
			document.removeEventListener("selectionchange", update);
			document.removeEventListener("pointerup", update);
		};
	}, [container, identity]);
	if (snapshot.current.identity !== identity || !selected)
		snapshot.current = { value, identity };
	return snapshot.current.value;
}
