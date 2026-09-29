import { create } from "zustand";
import type { ShareInvite } from "../../shared/share-deeplink";

// Open-state for the two halves of read-only session sharing: the owner's
// "Share read-only" dialog (opened from a cloud session's ⋮ menu) and the
// recipient's consent dialog (opened by an ao-app://share deep link). Both
// dialogs are mounted once in the shell and driven through this store.

export type ShareSessionTarget = {
	orgId: string;
	sessionId: string;
	title: string;
};

type ShareDialogState = {
	target: ShareSessionTarget | null;
	openShareSession: (target: ShareSessionTarget) => void;
	closeShareSession: () => void;
	invite: ShareInvite | null;
	setInvite: (invite: ShareInvite) => void;
	clearInvite: () => void;
	/** "Open share link…" paste box: the in-app alternative to the OS opening ao-app:// links. */
	pasteOpen: boolean;
	setPasteOpen: (open: boolean) => void;
};

export const useShareDialogStore = create<ShareDialogState>((set) => ({
	target: null,
	openShareSession: (target) => set({ target }),
	closeShareSession: () => set({ target: null }),
	invite: null,
	setInvite: (invite) => set({ invite, pasteOpen: false }),
	clearInvite: () => set({ invite: null }),
	pasteOpen: false,
	setPasteOpen: (pasteOpen) => set({ pasteOpen }),
}));
