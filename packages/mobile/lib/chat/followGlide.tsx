import { forwardRef, useCallback, useEffect, useMemo, useRef, type ComponentProps, type Ref } from "react";
import { StyleSheet } from "react-native";
import { KeyboardChatScrollView } from "react-native-keyboard-controller";
import Animated, {
	runOnJS,
	scrollTo,
	useAnimatedRef,
	useAnimatedStyle,
	useEvent,
	useFrameCallback,
	useSharedValue,
	type AnimatedRef,
	type SharedValue,
} from "react-native-reanimated";
import { glideStep } from "./timelineScroll";

type ChatScrollProps = ComponentProps<typeof KeyboardChatScrollView>;
type ScrollHost = Animated.ScrollView;

/**
 * The chat's scroll view with the follow glide's ref beside the list's own.
 * VirtualizedList clones its scroll component with its ref, so a ref given in
 * `renderScrollComponent` would be dropped; this one takes the list's ref and
 * hands the view to both.
 */
export const FollowScrollView = forwardRef(function FollowScrollView(
	{ followRef, ...props }: ChatScrollProps & { followRef: AnimatedRef<ScrollHost> },
	ref: Ref<ScrollHost>,
) {
	const both = useCallback((view: ScrollHost | null) => {
		followRef(view);
		if (typeof ref === "function") ref(view);
		else if (ref) (ref as { current: ScrollHost | null }).current = view;
	}, [followRef, ref]);
	return <KeyboardChatScrollView {...props} ref={both} />;
});

const SCROLL_EVENTS = ["onScroll", "onScrollBeginDrag", "onScrollEndDrag", "onMomentumScrollBegin", "onMomentumScrollEnd"] as const;
type ScrollEvent = { eventName: string; contentOffset: { y: number } };

export type FollowGlide = {
	scrollRef: AnimatedRef<ScrollHost>;
	/** Glides to `target`; one already under way takes the new target without starting over. */
	glideTo(target: number, reach: number): void;
	/** The list was put at `offset` by something else (an instant scroll of ours, a drag that came to rest). */
	place(offset: number): void;
	stop(): void;
	/** Whether a glide is moving the list. */
	active(): boolean;
	/** Forces a commit each frame the glide moves (see `commit`). */
	commitStyle: ReturnType<typeof useAnimatedStyle>;
};

/**
 * Follows the end of a conversation that is growing under the reader on the UI
 * thread, so the list moves with each line rather than stepping a line at a
 * time, and keeps one continuous motion however often the end moves: a new
 * target only changes where the glide is heading.
 *
 * It owns the list only while nobody else does. The person's finger stops it
 * on the UI thread in the frame the drag starts (and JS hears of the drag too),
 * and while the keyboard moves the list it waits; the screen asks again once
 * the keyboard settles.
 */
export function useFollowGlide(keyboardMoving: SharedValue<boolean>, onKeyboardTook: () => void): FollowGlide {
	const scrollRef = useAnimatedRef<ScrollHost>();
	const target = useSharedValue(0);
	// Where the glide has put the list, or where the list was last seen while no glide ran.
	const position = useSharedValue(0);
	const gliding = useSharedValue(false);
	const touching = useSharedValue(false);
	const reach = useSharedValue(0);
	// Bumped by every glide asked for, so a glide that came to rest cannot stop the frames of a newer one.
	const ticket = useSharedValue(0);
	const parked = useSharedValue(true);
	const asked = useRef(0);

	const frames = useFrameCallback((frame) => {
		"worklet";
		// Stopped by a drag, by the screen, or arrived: the frames stop until the next glide.
		const park = (toKeyboard: boolean) => {
			gliding.value = false;
			if (parked.value) return;
			parked.value = true;
			runOnJS(handOff)(ticket.value, toKeyboard);
		};
		if (!gliding.value || touching.value) return park(false);
		// keyboard-controller moves the list with the keys; follow again once they stop.
		if (keyboardMoving.value) return park(true);
		const next = glideStep(position.value, target.value, frame.timeSincePreviousFrame ?? 16, reach.value);
		if (next === position.value) return park(false);
		scrollTo(scrollRef, 0, next, false);
		position.value = next;
	}, false);
	function handOff(endedTicket: number, toKeyboard: boolean) {
		if (endedTicket !== asked.current) return;
		frames.setActive(false);
		if (toKeyboard) onKeyboardTook();
	}

	// The list's own movements, read on the UI thread: where it is while no glide
	// runs, and the person's drag, which ends a glide before its next frame.
	const watch = useEvent<ScrollEvent>((event) => {
		"worklet";
		if (event.eventName.endsWith("onScrollBeginDrag")) {
			touching.value = true;
			gliding.value = false;
		} else if (event.eventName.endsWith("onScrollEndDrag")) touching.value = false;
		if (!gliding.value) position.value = event.contentOffset.y;
	}, SCROLL_EVENTS as unknown as string[]) as unknown as { workletEventHandler?: { registerForEvents(tag: number): void; unregisterFromEvents(tag: number): void } };
	useEffect(() => scrollRef.observe((tag) => {
		const handler = watch.workletEventHandler;
		// Reanimated's own scroll-offset hook registers the same way; should an
		// upgrade move it, the glide loses track of the list and starts from stale
		// offsets, so say so in development rather than fail quietly.
		if (__DEV__ && !handler) console.warn("useFollowGlide: Reanimated no longer exposes workletEventHandler; follow glides cannot track the list's own scrolls.");
		if (!tag || !handler) return;
		handler.registerForEvents(tag as number);
		return () => handler.unregisterFromEvents(tag as number);
	}), [scrollRef, watch]);

	// Reanimated applies a scrollTo from a frame callback with the next commit;
	// a style that changes with every step makes that commit this frame
	// (keyboard-controller's chat scroll view does the same for its own).
	const commitStyle = useAnimatedStyle(() => ({ transform: [{ translateY: position.value }] }));

	return useMemo(() => ({
		scrollRef,
		commitStyle,
		glideTo(next: number, distance: number) {
			asked.current += 1;
			ticket.value = asked.current;
			parked.value = false;
			target.value = next;
			reach.value = distance;
			gliding.value = true;
			frames.setActive(true);
		},
		place(offset: number) {
			gliding.value = false;
			position.value = offset;
		},
		stop() {
			gliding.value = false;
		},
		active() {
			return gliding.value;
		},
	}), [commitStyle, frames, gliding, parked, position, reach, scrollRef, target, ticket]);
}

export const glideStyles = StyleSheet.create({
	commit: { display: "none", position: "absolute" },
});
