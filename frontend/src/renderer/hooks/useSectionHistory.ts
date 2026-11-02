import { useCallback, useState } from "react";

/** Local navigation for pages whose sections live outside router history. */
export function useSectionHistory<T extends string>(initial: T) {
	const [history, setHistory] = useState<{ past: T[]; current: T; future: T[] }>({ past: [], current: initial, future: [] });
	const reset = useCallback((current: T) => setHistory({ past: [], current, future: [] }), []);
	const visit = useCallback((current: T) => {
		setHistory((state) => state.current === current ? state : { past: [...state.past, state.current], current, future: [] });
	}, []);
	const back = useCallback(() => {
		setHistory((state) => {
			const current = state.past.at(-1);
			return current === undefined ? state : { past: state.past.slice(0, -1), current, future: [state.current, ...state.future] };
		});
	}, []);
	const forward = useCallback(() => {
		setHistory((state) => {
			const current = state.future[0];
			return current === undefined ? state : { past: [...state.past, state.current], current, future: state.future.slice(1) };
		});
	}, []);
	return { current: history.current, previous: history.past.at(-1), next: history.future[0], visit, back, forward, reset };
}
