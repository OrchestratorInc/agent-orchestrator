import { useEffect, useRef } from "react";
import { aoBridge } from "../../lib/bridge";
import {
	DICTATION_METER_BARS,
	dictationMeterSteps,
	drawDictationBars,
	runDictationMeter,
} from "../../../shared/dictation-meter";

/**
 * The microphone level as a row of bars that grow up and down from the
 * middle: tallest in the centre, with a gentle standing wave across them.
 * Drawn straight to the bars each frame, outside React.
 */
export function DictationMeter({ listening }: { listening: boolean }) {
	const row = useRef<HTMLSpanElement>(null);

	useEffect(() => {
		const bars = Array.from(row.current?.children ?? []) as HTMLElement[];
		const draw = (steps: number[]) => drawDictationBars(bars, steps);
		if (!listening) {
			draw(dictationMeterSteps(0, 0));
			return;
		}
		return runDictationMeter(aoBridge.dictation.onEvent, draw);
	}, [listening]);

	return (
		<span ref={row} aria-hidden="true" className="flex h-5 items-center gap-0.5">
			{Array.from({ length: DICTATION_METER_BARS }, (_, index) => (
				<span key={index} className="h-[3px] w-[3px] rounded-full bg-current" />
			))}
		</span>
	);
}
