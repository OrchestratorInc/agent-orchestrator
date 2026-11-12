import { useEffect, useRef } from "react";
import { aoBridge } from "../../lib/bridge";
import { cn } from "../../lib/utils";
import { DICTATION_METER_BARS, dictationMeterSteps, runDictationMeter } from "../../../shared/dictation-meter";

/** Bar heights, 1px apart, from the resting dot to the pill's inner height. */
const HEIGHTS = ["h-[3px]", "h-[4px]", "h-[5px]", "h-[6px]", "h-[7px]", "h-[8px]", "h-[9px]", "h-[10px]", "h-[11px]", "h-[12px]", "h-[13px]", "h-[14px]", "h-[15px]", "h-[16px]", "h-[17px]", "h-[18px]", "h-[19px]", "h-[20px]"];
const BAR = "w-[3px] rounded-full bg-current";

/**
 * The microphone level as a row of bars that grow up and down from the
 * middle: tallest in the centre, with a gentle standing wave across them.
 * Drawn straight to the bars each frame, outside React.
 */
export function DictationMeter({ listening }: { listening: boolean }) {
	const row = useRef<HTMLSpanElement>(null);

	useEffect(() => {
		const bars = Array.from(row.current?.children ?? []) as HTMLElement[];
		const draw = (steps: number[]) => {
			bars.forEach((bar, index) => {
				const className = `${BAR} ${HEIGHTS[steps[index]]}`;
				if (bar.className !== className) bar.className = className;
			});
		};
		if (!listening) {
			draw(dictationMeterSteps(0, 0));
			return;
		}
		return runDictationMeter(aoBridge.dictation.onEvent, draw);
	}, [listening]);

	return (
		<span ref={row} aria-hidden="true" className="flex h-5 items-center gap-0.5">
			{Array.from({ length: DICTATION_METER_BARS }, (_, index) => (
				<span key={index} className={cn(BAR, HEIGHTS[0])} />
			))}
		</span>
	);
}
