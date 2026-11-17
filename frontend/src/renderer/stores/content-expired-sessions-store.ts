import { create } from "zustand";

/**
 * Cloud sessions the control plane refused to restore because they are past
 * their creator's data retention (410 SESSION_CONTENT_EXPIRED): their content
 * is being removed, so Restore is no longer offered for them. The session
 * listing carries no such flag (a session leaves it once its content is
 * gone), so the refusal is what tells. Kept for this run of the app only.
 */
type ContentExpiredSessionsState = {
	ids: Readonly<Record<string, true>>;
	mark: (sessionId: string) => void;
};

export const useContentExpiredSessionsStore = create<ContentExpiredSessionsState>((set) => ({
	ids: {},
	mark: (sessionId) => set((state) => (state.ids[sessionId] ? state : { ids: { ...state.ids, [sessionId]: true } })),
}));

/** The control plane refused to restore this Cloud session for its data retention. */
export function useSessionContentExpired(sessionId: string | undefined): boolean {
	return useContentExpiredSessionsStore((state) => sessionId !== undefined && state.ids[sessionId] === true);
}
