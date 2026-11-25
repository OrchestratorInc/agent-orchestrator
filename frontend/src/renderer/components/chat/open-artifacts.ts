import { createContext, useContext } from "react";

/**
 * Opens the session's Artifacts tab, for the chat's "Open Artifacts". Provided
 * where the chat sits beside an inspector; absent elsewhere, so the link hides.
 */
export const OpenArtifactsContext = createContext<(() => void) | undefined>(undefined);

export function useOpenArtifacts(): (() => void) | undefined {
	return useContext(OpenArtifactsContext);
}
