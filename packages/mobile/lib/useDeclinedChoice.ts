import { useEffect, useState } from "react";

/**
 * A native picker shows the option it was given once the user taps one, even
 * when the screen does not apply it: they kept the current setting at a
 * confirmation, or saving failed. This counts those declined choices; key the
 * picker on `revision` and it remounts on the value the screen still holds.
 *
 * Call `choose` with the tapped value alongside the change handler. Once the
 * screen is no longer busy, a choice that did not become `current` was declined.
 */
export function useDeclinedChoice(current: string, busy: boolean): { revision: number; choose: (value: string) => void } {
	const [choice, setChoice] = useState<string>();
	const [revision, setRevision] = useState(0);
	useEffect(() => {
		if (choice === undefined || busy) return;
		if (choice !== current) setRevision((value) => value + 1);
		setChoice(undefined);
	}, [busy, choice, current]);
	return { revision, choose: setChoice };
}
