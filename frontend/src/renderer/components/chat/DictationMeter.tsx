import { useEffect, useRef } from "react";
import { aoBridge } from "../../lib/bridge";
import { cn } from "../../lib/utils";

const BARS = 11;
const MIDDLE = (BARS - 1) / 2;
const HEIGHTS = ["h-[3px]", "h-[4px]", "h-[5px]", "h-[6px]", "h-[7px]", "h-[8px]", "h-[9px]", "h-[10px]", "h-[11px]", "h-[12px]", "h-[13px]", "h-[14px]", "h-[15px]", "h-[16px]", "h-[17px]", "h-[18px]", "h-[19px]", "h-[20px]"];
const BAR = "w-[3px] rounded-full bg-current";
const REACH = Array.from({ length: BARS }, (_, bar) => {
	const distance = Math.abs(bar - MIDDLE) / MIDDLE;
	return 1 - 0.65 * distance * distance;
});
const NOISE_FLOOR = 0.2;
const SPEECH_SPAN = 0.6;
const ATTACK_S = 0.03;
const RELEASE_S = 0.14;
const WAVE_DEPTH = 0.22;
const WAVE_HZ = 1.6;

export function DictationMeter({ listening }: { listening: boolean }) {
	const row = useRef<HTMLSpanElement>(null);

	useEffect(() => {
		const bars = Array.from(row.current?.children ?? []) as HTMLElement[];
		const draw = (level: number, seconds: number) => {
			const swing = Math.cos(seconds * WAVE_HZ * 2 * Math.PI);
			bars.forEach((bar, index) => {
				const wave = 1 - WAVE_DEPTH * (0.5 + 0.5 * Math.sin((index / (BARS - 1)) * 3 * Math.PI) * swing);
				const step = Math.round(level * REACH[index] * wave * (HEIGHTS.length - 1));
				const className = `${BAR} ${HEIGHTS[step]}`;
				if (bar.className !== className) bar.className = className;
			});
		};
		if (!listening) {
			draw(0, 0);
			return;
		}

		let loudness = 0;
		const unsubscribe = aoBridge.dictation.onEvent((event) => {
			if (event.type === "levels") loudness = Math.max(loudness, ...event.values);
		});
		let position = 0;
		let last = performance.now();
		let frame = 0;
		const tick = (now: number) => {
			const dt = Math.min((now - last) / 1000, 0.05);
			last = now;
			const speech = Math.min(Math.max((loudness - NOISE_FLOOR) / SPEECH_SPAN, 0), 1);
			loudness = 0;
			position += (speech - position) * (1 - Math.exp(-dt / (speech > position ? ATTACK_S : RELEASE_S)));
			draw(position, now / 1000);
			frame = requestAnimationFrame(tick);
		};
		frame = requestAnimationFrame(tick);
		return () => {
			cancelAnimationFrame(frame);
			unsubscribe();
		};
	}, [listening]);

	return (
		<span ref={row} aria-hidden="true" className="flex h-5 items-center gap-0.5">
			{Array.from({ length: BARS }, (_, index) => (
				<span key={index} className={cn(BAR, HEIGHTS[0])} />
			))}
		</span>
	);
}
