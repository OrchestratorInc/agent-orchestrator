import { createContext, useContext } from "react";

export const FilesTopbarHostContext = createContext<HTMLElement | null>(null);

export function useFilesTopbarHost(): HTMLElement | null {
	return useContext(FilesTopbarHostContext);
}

export const FilesActionsHostContext = createContext<HTMLElement | null>(null);

export function useFilesActionsHost(): HTMLElement | null {
	return useContext(FilesActionsHostContext);
}
