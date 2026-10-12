import { useEffect, useRef } from "react";

const FALLBACK_ATTR = "data-tab-stop-fallback";

/**
 * Keeps the selected tab visible: when selection moves (⌃Tab, arrows, a
 * shortcut) in a strip that scrolls horizontally, scroll it into view.
 *
 * Roving-tabindex fallback: when no tab in the list is active (so none has
 * tabindex 0), make the first tab tabbable. The fallback is stripped and
 * recomputed on every render so it never leaves a second tab stop behind.
 */
export function useTabListTabStop<T extends HTMLElement>() {
	const ref = useRef<T | null>(null);
	const lastSelected = useRef<Element | null>(null);
	useEffect(() => {
		const list = ref.current;
		if (!list) return;
		const selected = list.querySelector('[role="tab"][aria-selected="true"]');
		if (selected && selected !== lastSelected.current) {
			(selected as HTMLElement).scrollIntoView?.({ block: "nearest", inline: "nearest" });
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
