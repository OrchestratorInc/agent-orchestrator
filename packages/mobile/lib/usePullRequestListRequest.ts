import { useCallback, useEffect, useRef, useState } from "react";

/**
 * A session screen's ask for its PR list, which lives over the composer
 * (ChatComposer's `pullRequestListRequest`). From a view with no composer (a
 * Cloud session's terminal) it shows the chat first and asks once that is up,
 * so the composer mounts before the request it has to answer.
 */
export function usePullRequestListRequest(showingComposer: boolean, showComposer: () => void) {
	const [request, setRequest] = useState(0);
	const pending = useRef(false);
	useEffect(() => {
		if (!showingComposer || !pending.current) return;
		pending.current = false;
		setRequest((value) => value + 1);
	}, [showingComposer]);
	const ask = useCallback(() => {
		if (showingComposer) {
			setRequest((value) => value + 1);
			return;
		}
		pending.current = true;
		showComposer();
	}, [showComposer, showingComposer]);
	return { request, ask };
}
