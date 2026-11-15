import { create } from "zustand";

type LocalSignInDialogState = {
	open: boolean;
	openDialog: () => void;
	closeDialog: () => void;
	setOpen: (open: boolean) => void;
};

export const useLocalSignInDialogStore = create<LocalSignInDialogState>((set) => ({
	open: false,
	openDialog: () => set({ open: true }),
	closeDialog: () => set({ open: false }),
	setOpen: (open) => set({ open }),
}));
