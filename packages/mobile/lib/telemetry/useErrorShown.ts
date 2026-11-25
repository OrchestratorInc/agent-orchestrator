import { useFocusEffect } from "expo-router";
import { useCallback } from "react";
import type { ErrorKind, ErrorPresentation } from "./uiUsage";
import { reportErrorShown } from "./uiUsageRegistry";

/**
 * Reports an error view while its screen has focus: when the screen gains
 * focus showing it, or when its kind changes while focused. Board tabs stay
 * mounted, so a hidden tab must not report under the visible tab's name.
 * Null while no error shows. The tracker drops a repeat within five minutes.
 */
export function useErrorShown(kind: ErrorKind | null, presentation: ErrorPresentation = "screen"): void {
	useFocusEffect(useCallback(() => {
		if (kind) reportErrorShown(presentation, kind);
	}, [kind, presentation]));
}
