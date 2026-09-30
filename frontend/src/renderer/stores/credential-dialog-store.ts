import { create } from "zustand";

// Open-state for the cloud harness login dialog. Harness rows on the Harnesses
// settings page open it scoped to their agent; it is the only place cloud
// agent logins are made.
type CredentialDialogState = {
	open: boolean;
	targetAgent: string | null;
	openDialog: (agent: string) => void;
	closeDialog: () => void;
};

export const useCredentialDialogStore = create<CredentialDialogState>((set) => ({
	open: false,
	targetAgent: null,
	openDialog: (agent) => set({ open: true, targetAgent: agent }),
	// Keep targetAgent while closed so the closing animation keeps its content.
	closeDialog: () => set({ open: false }),
}));
