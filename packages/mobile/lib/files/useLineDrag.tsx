import { forwardRef, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { View, type LayoutChangeEvent, type NativeScrollEvent, type NativeSyntheticEvent, type StyleProp, type ViewStyle } from "react-native";
import { Gesture } from "react-native-gesture-handler";
import { haptics } from "../haptics";

/** How close to the top edge a drag starts scrolling the list up. */
const EDGE_TOP = 56;
/**
 * The bottom zone is deeper: the last strip of the screen sits over the home
 * indicator / gesture bar, where a finger cannot comfortably rest.
 */
const EDGE_BOTTOM = 110;
/** Fastest auto-scroll, in points per frame, reached at the very edge. */
const MAX_SPEED = 18;

type Frame = { y: number; height: number };

/**
 * Click-and-drag line selection for a virtualized list, the phone version of
 * dragging across lines on desktop.
 *
 * - Hold a line, then drag: the selection follows the finger, like selecting text.
 * - Drag the line numbers: selects straight away, no hold, since a vertical drag
 *   there has no other meaning (the code area still scrolls).
 * - Near the top or bottom edge the list scrolls under the finger.
 *
 * Rows are found by position: each cell reports its frame through
 * `CellRendererComponent`, and the gesture's y plus the scroll offset picks the
 * row under the finger.
 */
export function useLineDrag({
	listRef,
	gutterWidth,
	enabled,
	onStart,
	onMove,
	onEnd,
}: {
	listRef: React.RefObject<{ scrollToOffset(params: { offset: number; animated?: boolean }): void } | null>;
	gutterWidth: number;
	enabled: boolean;
	onStart(index: number): void;
	onMove(index: number): void;
	onEnd(): void;
}) {
	const frames = useRef(new Map<number, Frame>());
	const scrollY = useRef(0);
	const viewport = useRef(0);
	const content = useRef(0);
	const lastY = useRef(0);
	const focus = useRef(-1);
	const frame = useRef<number | null>(null);
	const [dragging, setDragging] = useState(false);
	const active = useRef(false);
	const callbacks = useRef({ onStart, onMove, onEnd });
	callbacks.current = { onStart, onMove, onEnd };

	const indexAt = useCallback((y: number): number => {
		const target = scrollY.current + y;
		let nearest = -1;
		let distance = Infinity;
		for (const [index, cell] of frames.current) {
			if (target >= cell.y && target < cell.y + cell.height) return index;
			const gap = target < cell.y ? cell.y - target : target - (cell.y + cell.height);
			if (gap < distance) { distance = gap; nearest = index; }
		}
		return nearest;
	}, []);

	const track = useCallback((y: number) => {
		lastY.current = y;
		const index = indexAt(y);
		if (index >= 0 && index !== focus.current) {
			focus.current = index;
			callbacks.current.onMove(index);
		}
	}, [indexAt]);

	const stopScroll = useCallback(() => {
		if (frame.current != null) cancelAnimationFrame(frame.current);
		frame.current = null;
	}, []);

	// While the finger rests near an edge, keep scrolling and keep extending.
	const autoScroll = useCallback(() => {
		frame.current = null;
		if (!active.current) return;
		const y = lastY.current;
		const speed = y < EDGE_TOP
			? -MAX_SPEED * (1 - Math.max(0, y) / EDGE_TOP)
			: y > viewport.current - EDGE_BOTTOM
				? MAX_SPEED * Math.max(0.25, 1 - Math.max(0, viewport.current - y) / EDGE_BOTTOM)
				: 0;
		if (!speed) return;
		const max = Math.max(0, content.current - viewport.current);
		const next = Math.min(max, Math.max(0, scrollY.current + speed));
		if (next !== scrollY.current) {
			scrollY.current = next;
			listRef.current?.scrollToOffset({ offset: next, animated: false });
			track(y);
		}
		frame.current = requestAnimationFrame(autoScroll);
	}, [listRef, track]);

	const begin = useCallback((y: number) => {
		const index = indexAt(y);
		if (index < 0) return;
		active.current = true;
		focus.current = index;
		lastY.current = y;
		setDragging(true);
		haptics.select();
		callbacks.current.onStart(index);
	}, [indexAt]);

	const move = useCallback((y: number) => {
		if (!active.current) return;
		track(y);
		if (frame.current == null && (y < EDGE_TOP || y > viewport.current - EDGE_BOTTOM)) frame.current = requestAnimationFrame(autoScroll);
	}, [autoScroll, track]);

	const finish = useCallback(() => {
		if (!active.current) return;
		active.current = false;
		stopScroll();
		setDragging(false);
		haptics.select();
		callbacks.current.onEnd();
	}, [stopScroll]);

	useEffect(() => stopScroll, [stopScroll]);

	const gesture = useMemo(() => {
		const hold = Gesture.Pan()
			.runOnJS(true)
			.enabled(enabled)
			.activateAfterLongPress(260)
			.onStart((event) => begin(event.y))
			.onUpdate((event) => move(event.y))
			.onFinalize(() => finish());
		const gutter = Gesture.Pan()
			.runOnJS(true)
			.enabled(enabled)
			.hitSlop({ left: 0, width: gutterWidth })
			.activeOffsetY([-6, 6])
			.failOffsetX([-24, 24])
			.onStart((event) => begin(event.y))
			.onUpdate((event) => move(event.y))
			.onFinalize(() => finish());
		return Gesture.Race(gutter, hold);
	}, [begin, enabled, finish, gutterWidth, move]);

	const CellRenderer = useMemo(() => forwardRef<View, { index: number; style?: StyleProp<ViewStyle>; onLayout?: (event: LayoutChangeEvent) => void; children?: ReactNode }>(
		function LineCell({ index, style, onLayout, children, ...rest }, ref) {
			return (
				<View
					ref={ref}
					style={style}
					{...rest}
					onLayout={(event) => {
						frames.current.set(index, { y: event.nativeEvent.layout.y, height: event.nativeEvent.layout.height });
						onLayout?.(event);
					}}
				>
					{children}
				</View>
			);
		},
	), []);

	const listProps = useMemo(() => ({
		CellRendererComponent: CellRenderer,
		// While a drag auto-scrolls, the drag owns the offset; scroll events lag a
		// frame behind and would pull it back.
		onScroll: (event: NativeSyntheticEvent<NativeScrollEvent>) => { if (!active.current) scrollY.current = event.nativeEvent.contentOffset.y; },
		scrollEventThrottle: 16,
		onLayout: (event: LayoutChangeEvent) => { viewport.current = event.nativeEvent.layout.height; },
		onContentSizeChange: (_width: number, height: number) => { content.current = height; },
	}), [CellRenderer]);

	/** Row frames belong to one data set; drop them when the rows change. */
	const reset = useCallback(() => { frames.current.clear(); }, []);

	return { gesture, dragging, listProps, reset };
}
