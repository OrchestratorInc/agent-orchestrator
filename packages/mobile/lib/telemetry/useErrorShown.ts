import { useEffect } from "react";
import type { ErrorKind, ErrorPresentation } from "./uiUsage";
import { reportErrorShown } from "./uiUsageRegistry";

/**
 * Reports an error view each time it comes on screen with a new kind; null
 * while no error shows. The tracker drops a repeat within five minutes.
 */
export function useErrorShown(kind: ErrorKind | null, presentation: ErrorPresentation = "screen"): void {
	useEffect(() => {
		if (kind) reportErrorShown(presentation, kind);
	}, [kind, presentation]);
}
