import { createContext, useContext } from "react";

const VIEWPORTS = [
	{ embedded: false, visible: false },
	{ embedded: false, visible: true },
	{ embedded: true, visible: false },
	{ embedded: true, visible: true },
] as const;
export const sessionViewportState = (embedded: boolean, visible: boolean) =>
	VIEWPORTS[Number(embedded) * 2 + Number(visible)];

/** Presentation only: opening a viewport never starts or resumes an agent. */
export const SessionViewportContext = createContext({ embedded: false, visible: true });
export const useSessionViewport = () => useContext(SessionViewportContext);
