import { useCallback, useEffect, useState } from "react";
import { aoBridge } from "../lib/bridge";

/** Keep the native quit request usable on both onboarding and the app shell. */
export function useQuitConfirmation() {
	const [open, setOpen] = useState(false);
	const [keepsRunning, setKeepsRunning] = useState(false);
	useEffect(() => aoBridge.app.onQuitConfirmRequest((request) => {
		setKeepsRunning(request.keepsRunning);
		setOpen(true);
	}), []);
	const onResolve = useCallback((confirmed: boolean) => {
		setOpen(false);
		aoBridge.app.resolveQuitConfirm(confirmed);
	}, []);
	return { open, keepsRunning, onResolve };
}
