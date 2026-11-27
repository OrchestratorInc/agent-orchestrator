import { create } from "zustand";
import type { SharedArtifactCopy, SharedArtifactSessionKey } from "../../preload";

/**
 * A copy of a link someone shared with the person, waiting for the new
 * standalone session the draft starts. The copy goes with that session's
 * spawn, into its artifact folder before the agent starts; the session is then
 * remembered as the one that works from the link. Held in memory only: a
 * reload drops it, and the next "Start a session with this" copies again.
 */
export interface PendingSharedArtifact {
	key: SharedArtifactSessionKey;
	name: string;
	/** The link's updatedAt when it was copied. */
	updatedAt: string;
	copy: SharedArtifactCopy;
	/**
	 * The line written into the draft for it, as first written. The copy goes
	 * with the new-chat page's next standalone send whether or not the person
	 * kept the line: the chip shows it is attached until removed.
	 */
	prompt: string;
}

interface SharedArtifactDraftState {
	pending: PendingSharedArtifact | null;
	setPending: (pending: PendingSharedArtifact) => void;
	clearPending: () => void;
}

export const useSharedArtifactDraftStore = create<SharedArtifactDraftState>((set) => ({
	pending: null,
	setPending: (pending) => set({ pending }),
	clearPending: () => set({ pending: null }),
}));
