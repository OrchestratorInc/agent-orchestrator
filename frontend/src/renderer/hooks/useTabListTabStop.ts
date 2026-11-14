import { useEffect, useRef } from "react";

const FALLBACK_ATTR = "data-tab-stop-fallback";

export function useTabListTabStop<T extends HTMLElement>() {
	const ref = useRef<T | null>(null);
	const lastSelected = useRef<Element | null>(null);
	const scrollFrame = useRef<number | null>(null);
	useEffect(() => () => {
		if (scrollFrame.current !== null) cancelAnimationFrame(scrollFrame.current);
	}, []);
	useEffect(() => {
		const list = ref.current;
		if (!list) return;
		const selected = list.querySelector('[role="tab"][aria-selected="true"]');
		if (selected && selected !== lastSelected.current) {
			if (scrollFrame.current !== null) cancelAnimationFrame(scrollFrame.current);
			scrollFrame.current = requestAnimationFrame(() => {
				scrollFrame.current = null;
				const current = list.querySelector('[role="tab"][aria-selected="true"]');
				(current as HTMLElement | null)?.scrollIntoView?.({ block: "nearest", inline: "nearest" });
			});
		}
		lastSelected.current = selected;
		list.querySelectorAll<HTMLElement>(`[${FALLBACK_ATTR}]`).forEach((el) => {
			el.removeAttribute(FALLBACK_ATTR);
			el.tabIndex = -1;
		});
		const tabs = list.querySelectorAll<HTMLElement>('[role="tab"]');
		if (tabs.length === 0) return;
		if (Array.from(tabs).some((tab) => tab.tabIndex === 0)) return;
		tabs[0].setAttribute(FALLBACK_ATTR, "");
		tabs[0].tabIndex = 0;
	});
	return ref;
}
