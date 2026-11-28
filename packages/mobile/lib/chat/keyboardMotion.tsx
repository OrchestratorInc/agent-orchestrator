import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";
import { useKeyboardHandler, useReanimatedFocusedInput } from "react-native-keyboard-controller";
import { runOnJS, useSharedValue, type SharedValue } from "react-native-reanimated";

/**
 * The keyboard's movements as the chat screen's parts read them, from one
 * keyboard handler.
 *
 * Every keyboard handler runs for every keyboard event, wanted or not, on the
 * UI thread in the frame the keyboard moves. The composer, the view that holds
 * it to the keyboard and the conversation had one each, on top of
 * keyboard-controller's own, and the first frame of a close, already the
 * slowest, ran all of them.
 */
export type KeyboardMotion = {
	/** From a movement's start to its end. */
	moving: SharedValue<boolean>;
	/**
	 * Whether the movement under way, or the last one, opens the keyboard. The
	 * composer's follower clears it once the screen has settled the keyboard
	 * closed (see KeyboardFollower).
	 */
	opening: SharedValue<boolean>;
	/**
	 * The last movement is a close that started with no field focused: a tap
	 * elsewhere let go of the field, and the keyboard went because of it.
	 */
	closedByBlur: SharedValue<boolean>;
	/** Calls `listener` on JS each time a movement ends; returns the unsubscribe. */
	onEnd(listener: () => void): () => void;
};

const KeyboardMotionContext = createContext<KeyboardMotion | null>(null);

function useKeyboardMotionSource(): KeyboardMotion {
	const { input } = useReanimatedFocusedInput();
	const moving = useSharedValue(false);
	const opening = useSharedValue(false);
	const closedByBlur = useSharedValue(false);
	const [listeners] = useState(() => new Set<() => void>());
	const ended = useCallback(() => {
		for (const listener of [...listeners]) listener();
	}, [listeners]);
	useKeyboardHandler({
		onStart: (event) => {
			"worklet";
			moving.value = true;
			opening.value = event.height > 0;
			closedByBlur.value = event.height === 0 && input.value === null;
		},
		onEnd: () => {
			"worklet";
			moving.value = false;
			runOnJS(ended)();
		},
	}, [ended]);
	return useMemo(() => ({
		moving,
		opening,
		closedByBlur,
		onEnd: (listener: () => void) => {
			listeners.add(listener);
			return () => { listeners.delete(listener); };
		},
	}), [moving, opening, closedByBlur, listeners]);
}

function KeyboardMotionSource({ children }: { children: ReactNode }) {
	const motion = useKeyboardMotionSource();
	return <KeyboardMotionContext.Provider value={motion}>{children}</KeyboardMotionContext.Provider>;
}

/**
 * Shares one keyboard handler with everything inside it. Inside another scope
 * it adds nothing, so a part wrapped in its own scope (the composer, its
 * follower, the conversation) shares the screen's when there is one and has
 * its own when it stands alone.
 */
export function KeyboardMotionScope({ children }: { children: ReactNode }) {
	return useContext(KeyboardMotionContext) ? <>{children}</> : <KeyboardMotionSource>{children}</KeyboardMotionSource>;
}

export function useKeyboardMotion(): KeyboardMotion {
	const motion = useContext(KeyboardMotionContext);
	if (!motion) throw new Error("useKeyboardMotion is used outside a KeyboardMotionScope");
	return motion;
}
