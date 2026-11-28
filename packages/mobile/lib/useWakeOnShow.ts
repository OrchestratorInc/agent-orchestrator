import { useEffect, useRef } from "react";

/**
 * Starts a device that was put to sleep for going unused once it is shown, one
 * try per showing: a refused start (no room on the machine) says why and waits
 * for Start, or for the screen to be shown again.
 */
export function useWakeOnShow(asleep: boolean, shown: boolean, starting: boolean, start: () => void): void {
	const tried = useRef(false);
	useEffect(() => {
		if (!asleep || !shown) {
			tried.current = false;
			return;
		}
		if (starting || tried.current) return;
		tried.current = true;
		start();
	}, [asleep, shown, starting, start]);
}
