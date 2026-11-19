import { createContext, useContext } from "react";

/**
 * The top-bar slot for the Files filter. Like the browser's address bar, the
 * Files panel renders its filter field into this element: the inspector's top
 * bar when docked, the maximized overlay's titlebar band when maximized. With
 * no host the field stays in the panel's own header.
 */
export const FilesTopbarHostContext = createContext<HTMLElement | null>(null);

export function useFilesTopbarHost(): HTMLElement | null {
	return useContext(FilesTopbarHostContext);
}

/**
 * The top-bar slot for the Files maximize button: the inspector top bar's
 * trailing edge, just before its pinned buttons. Without it the button follows
 * the filter into its host, or stays in the panel's header.
 */
export const FilesActionsHostContext = createContext<HTMLElement | null>(null);

export function useFilesActionsHost(): HTMLElement | null {
	return useContext(FilesActionsHostContext);
}
