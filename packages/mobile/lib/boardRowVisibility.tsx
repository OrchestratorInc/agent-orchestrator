import { createContext, useCallback, useContext, useSyncExternalStore, type ReactNode } from "react";
import { LatticeActiveContext } from "./chat/latticeActivity";

/**
 * Which of the board's rows are on screen, as the list reports them and the
 * board's focus allows.
 *
 * The board keeps a few screens of rows mounted either side of the viewport,
 * and a working row's lattice keeps a native loop running on the UI thread
 * (LatticeCometLoader). Off screen it holds still: a row reads only its own key, so
 * a row scrolling in or out re-renders that row's lattice and nothing else.
 *
 * Until the list first reports, every row counts as on screen. While the
 * board's screen is out of focus (another tab, a pushed screen), no row does;
 * the last report is kept, so the rows on screen resume as soon as the board
 * is focused again, without waiting for the list to report afresh.
 */
export type VisibleRows = {
	set(keys: ReadonlySet<string>): void;
	focus(focused: boolean): void;
	visible(key: string): boolean;
	subscribe(key: string, listener: () => void): () => void;
};

export function createVisibleRows(): VisibleRows {
	let shown: ReadonlySet<string> | undefined;
	let focused = true;
	const listeners = new Map<string, Set<() => void>>();
	const visible = (key: string) => focused && (shown === undefined || shown.has(key));
	// Applies a change, then tells each row whose visibility it changed.
	const update = (change: () => void) => {
		const before = new Map([...listeners.keys()].map((key) => [key, visible(key)]));
		change();
		for (const [key, set] of listeners) {
			if (before.get(key) !== visible(key)) for (const listener of set) listener();
		}
	};
	return {
		set(keys) {
			update(() => { shown = keys; });
		},
		focus(next) {
			update(() => { focused = next; });
		},
		visible,
		subscribe(key, listener) {
			const set = listeners.get(key) ?? new Set();
			set.add(listener);
			listeners.set(key, set);
			return () => {
				set.delete(listener);
				if (!set.size) listeners.delete(key);
			};
		},
	};
}

export const VisibleRowsContext = createContext<VisibleRows | null>(null);

/** Whether the board row `rowKey` is on screen; true outside a board. */
export function useRowVisible(rowKey: string): boolean {
	const rows = useContext(VisibleRowsContext);
	const subscribe = useCallback((listener: () => void) => rows?.subscribe(rowKey, listener) ?? (() => {}), [rowKey, rows]);
	const read = useCallback(() => rows?.visible(rowKey) ?? true, [rowKey, rows]);
	return useSyncExternalStore(subscribe, read, read);
}

/** A board row's own scope: its lattice animates only while the row is on screen. */
export function BoardRowScope({ rowKey, children }: { rowKey: string; children: ReactNode }) {
	const visible = useRowVisible(rowKey);
	return <LatticeActiveContext.Provider value={visible}>{children}</LatticeActiveContext.Provider>;
}
