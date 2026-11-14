import { useRef } from "react";
import { useUiStore } from "../stores/ui-store";

export function useAgentManagementMenu(focusAgentId?: string, hostId?: string, harnessView?: "local" | "cloud") {
	const triggerRef = useRef<HTMLButtonElement>(null);
	const pending = useRef(false);
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);
	return {
		triggerRef,
		requestManagement: () => { pending.current = true; },
		onCloseAutoFocus: (event: Event) => {
			if (!pending.current) return;
			pending.current = false;
			event.preventDefault();
			triggerRef.current?.focus({ preventScroll: true });
			openGlobalSettings("harness", { focusAgentId, ...(hostId ? { hostId } : {}), harnessView, preserveProject: true });
		},
	};
}
