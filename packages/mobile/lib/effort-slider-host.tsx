import { memo, useEffect, useState, useSyncExternalStore } from "react";
import type { EffortChoice } from "./modelEffort";
import { EffortGaugeButton } from "./effort-gauge";
import { EffortSliderOverlay } from "./effort-slider";
import { useEffortSliderOpen, useEffortSliderSwitchOpen, type EffortSliderSwitch } from "./useEffortSlider";

/*
 * A composer's effort gauge and slider, wired to the composer's switch (see
 * EffortSliderSwitch) rather than to its state: a tap on the gauge re-renders
 * the gauge and the slider, and the composer stays as it was.
 */

/** The speedometer that opens and closes the slider. `onPress` runs first. */
export function EffortSliderGauge({ slider, choices, disabled, onPress, testID }: {
	slider: EffortSliderSwitch;
	choices: readonly EffortChoice[];
	disabled: boolean;
	onPress?: () => void;
	testID: string;
}) {
	const open = useEffortSliderSwitchOpen(slider) && !disabled && choices.length > 0;
	return <EffortGaugeButton choices={choices} open={open} disabled={disabled} testID={testID} onPress={() => { onPress?.(); slider.setOpen(!open); }} />;
}

type HostProps = {
	slider: EffortSliderSwitch;
	choices: readonly EffortChoice[];
	/** Closes the slider while true (the task is sending, the agent is busy). */
	busy: boolean;
	onChoose: (value: string) => void;
	onRelease?: () => void;
	/** Hears every close, however it came about. */
	onClose?: () => void;
};

/**
 * The slider over the composer. Always mounted, so its closing rules (a tap
 * outside, the keyboard, back, the timeout) run while it is up. The slider
 * itself is built once the screen is idle after the composer appears, and
 * then kept, hidden, between opens: opening only reveals it, where building
 * its card, track and veil on the tap held the slider back. Memoised, so a
 * composer re-rendering under a streaming reply leaves it alone.
 */
export const EffortSliderHost = memo(function EffortSliderHost({ slider, choices, busy, onChoose, onRelease, onClose }: HostProps) {
	const { open } = useEffortSliderOpen(choices, busy, onClose, slider);
	// The composer reports its height to the switch as it lays out, so a
	// change re-renders this and not the composer.
	const bottom = useSyncExternalStore(slider.subscribe, slider.bottom);
	const warm = useWarmAfterIdle();
	return (open || warm) && bottom ? <EffortSliderOverlay visible={open} choices={choices} onChoose={onChoose} onRelease={onRelease} bottom={bottom} /> : null;
});

/**
 * True once the screen has gone idle after mounting: building the slider
 * then keeps it off both the tap that opens it and the screen's own first
 * frames. Where there is no idle callback it waits for the first open.
 */
export function useWarmAfterIdle(): boolean {
	const [warm, setWarm] = useState(false);
	useEffect(() => {
		if (typeof requestIdleCallback !== "function") return;
		const handle = requestIdleCallback(() => setWarm(true));
		return () => cancelIdleCallback(handle);
	}, []);
	return warm;
}
