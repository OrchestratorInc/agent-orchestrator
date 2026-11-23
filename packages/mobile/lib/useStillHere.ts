import { useCallback, useEffect, useRef } from "react";

export function useStillHere(key: string, isFocused: () => boolean) {
	const generation = useRef(0);
	const focused = useRef(isFocused);
	focused.current = isFocused;
	useEffect(() => () => { generation.current++; }, [key]);
	return useCallback(() => {
		const started = generation.current;
		return () => started === generation.current && focused.current();
	}, []);
}
