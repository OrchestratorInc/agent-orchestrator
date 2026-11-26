import { createContext, useCallback, useContext, useSyncExternalStore, type ReactNode } from "react";
import { LatticeActiveContext } from "./chat/latticeActivity";

/**
 * Which of the board's rows are on screen, as the list reports them.
 *
 * The board keeps a few screens of rows mounted either side of the viewport,
 * and a working row's lattice (sixteen dots, twelve animating) runs every frame
 * on the UI thread. Off screen it holds still: a row reads only its own key, so
 * a row scrolling in or out re-renders that row's lattice and nothing else.
 *
 * Until the list first reports, every row counts as on screen.
 */
export type VisibleRows = {
	set(keys: ReadonlySet<string>): void;
	visible(key: string): boolean;
	subscribe(key: string, listener: () => void): () => void;
};

export function createVisibleRows(): VisibleRows {
	let shown: ReadonlySet<string> | undefined;
	const listeners = new Map<string, Set<() => void>>();
	return {
		set(keys) {
			const before = shown;
			shown = keys;
			for (const [key, set] of listeners) {
				const was = before === undefined || before.has(key);
				if (was !== keys.has(key)) for (const listener of set) listener();
			}
		},
		visible: (key) => shown === undefined || shown.has(key),
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
