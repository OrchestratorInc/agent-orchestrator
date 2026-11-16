import { create } from "zustand";
import type { SessionTabAgentMenu } from "../components/TabContextMenu";

/**
 * Each mounted session view's interface switch (chat ↔ terminal), keyed by
 * session id, so Cmd+K can offer the switch for the session on screen.
 */
export const useSessionSwitchStore = create<{
	menus: Record<string, SessionTabAgentMenu>;
	publish: (sessionId: string, menu: SessionTabAgentMenu | undefined) => void;
}>((set) => ({
	menus: {},
	publish: (sessionId, menu) =>
		set((state) => {
			if (state.menus[sessionId] === menu) return state;
			const menus = { ...state.menus };
			if (menu) menus[sessionId] = menu;
			else delete menus[sessionId];
			return { menus };
		}),
}));
