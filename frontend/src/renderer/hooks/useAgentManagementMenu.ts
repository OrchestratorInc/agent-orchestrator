import { useRef } from "react";
import { useUiStore } from "../stores/ui-store";

/** Open Settings after the menu restores its trigger, so stacked dialogs retain focus. */
export function useAgentManagementMenu(focusAgentId?: string, hostId?: string, harnessView?: "local" | "cloud") {
	const triggerRef = useRef<HTMLButtonElement>(null);
	// The agent Settings opens on, or true for the hook's own.
	const pending = useRef<string | boolean>(false);
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);
	return {
		triggerRef,
		requestManagement: () => { pending.current = true; },
		/** Settings opens on this agent, to install it or sign in. */
		requestAgentSetup: (agentId: string) => { pending.current = agentId; },
		onCloseAutoFocus: (event: Event) => {
			if (!pending.current) return;
			const agent = typeof pending.current === "string" ? pending.current : focusAgentId;
			pending.current = false;
			event.preventDefault();
			triggerRef.current?.focus({ preventScroll: true });
			openGlobalSettings("harness", { focusAgentId: agent, ...(hostId ? { hostId } : {}), harnessView, preserveProject: true });
		},
	};
}
