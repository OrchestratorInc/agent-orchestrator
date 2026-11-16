import { Activity, type ReactElement, useRef } from "react";

/** The chat being shown plus the three before it. */
export const KEEP_RECENT_ALIVE_MAX = 4;

/**
 * Renders `children` for `activeKey` and keeps the last `max` keys' views
 * mounted but hidden, so switching back to a recent one reveals the view it
 * left instead of building it again. A hidden view keeps its state and DOM
 * (scroll position, composer draft, folded messages) and runs no effects, so it
 * holds no subscriptions or listeners until it is shown again. React hides it
 * with `display: none` on its top-level DOM nodes only: a portal rendered from
 * inside the view stays visible, so give each view its own portal container.
 *
 * Views render in the order they were first opened, never reordered: moving a
 * kept view's DOM would reset its scroll position.
 *
 * With `show` false the key's own view is dropped (it is showing something
 * else, and its kept view would be stale when it comes back) and every other
 * kept view stays hidden.
 */
export function KeepRecentAlive({
	activeKey,
	show = true,
	children,
	max = KEEP_RECENT_ALIVE_MAX,
}: {
	activeKey: string;
	show?: boolean;
	children?: ReactElement | null;
	max?: number;
}) {
	// Keyed by view, in first-opened order. A hidden view keeps the element it
	// last rendered with; only the active one gets the new props.
	const views = useRef(new Map<string, ReactElement>()).current;
	// Least recently shown first.
	const recent = useRef<string[]>([]).current;

	const at = recent.indexOf(activeKey);
	if (show && children) {
		views.set(activeKey, children);
		if (at !== recent.length - 1 || at === -1) {
			if (at !== -1) recent.splice(at, 1);
			recent.push(activeKey);
		}
	} else if (at !== -1) {
		views.delete(activeKey);
		recent.splice(at, 1);
	}
	while (recent.length > Math.max(1, max)) {
		const evicted = recent.shift();
		if (evicted !== undefined) views.delete(evicted);
	}

	return Array.from(views, ([key, view]) => (
		<Activity key={key} mode={show && key === activeKey ? "visible" : "hidden"}>
			{view}
		</Activity>
	));
}
