import { Image, Modal, Platform, StyleSheet, View, useWindowDimensions } from "react-native";
import { Gesture, GestureDetector, GestureHandlerRootView } from "react-native-gesture-handler";
import Animated, { useAnimatedStyle, useSharedValue, withSpring, withTiming } from "react-native-reanimated";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { scheduleOnRN } from "react-native-worklets";
import { backdropOpacity, boundTranslation, clamp, DOUBLE_TAP_ZOOM, MAX_ZOOM, MIN_ZOOM, shouldDismiss, zoomTranslation } from "./imageZoom";
import { NativeHeaderButton } from "./native-header-button";
import { useTheme } from "./ThemeProvider";
import { duration, space, spring } from "./tokens";
import { useReducedMotion } from "./useReducedMotion";

/** A local file, or a daemon URL that needs the connection's Bearer header. */
export type ImageViewerSource = { uri: string; headers?: Record<string, string> };

/** How far a pinch may squeeze past the fitted size before it springs back. */
const UNDERSHOOT = 0.8;

/**
 * Full-screen image at its own aspect ratio, over the page's own background as
 * Photos draws it. Pinch and double tap zoom, a drag moves the zoomed image,
 * and a drag or flick of the unzoomed one sends it away. The gestures run on
 * the UI thread, so they track the fingers on both platforms; a scroll view's
 * zoom does nothing on Android.
 */
export function ImageViewer({ visible, name, source, onClose }: { visible: boolean; name: string; source: ImageViewerSource; onClose(): void }) {
	return (
		<Modal visible={visible} transparent animationType="fade" statusBarTranslucent navigationBarTranslucent onRequestClose={onClose}>
			{/* A modal is a window of its own on Android, outside the app's gesture root. */}
			<GestureHandlerRootView style={styles.fill}>
				{visible ? <ZoomableImage name={name} source={source} onClose={onClose} /> : null}
			</GestureHandlerRootView>
		</Modal>
	);
}

function ZoomableImage({ name, source, onClose }: { name: string; source: ImageViewerSource; onClose(): void }) {
	const t = useTheme();
	const insets = useSafeAreaInsets();
	const { width, height } = useWindowDimensions();
	const reduceMotion = useReducedMotion();
	// RN Android forwards HTTP headers only for array sources.
	const imageSource = Platform.OS === "android" ? [source] : source;

	const scale = useSharedValue(1);
	const x = useSharedValue(0);
	const y = useSharedValue(0);
	const startScale = useSharedValue(1);
	const startX = useSharedValue(0);
	const startY = useSharedValue(0);
	/** The unzoomed image's drag towards closing. */
	const drag = useSharedValue(0);

	const settle = (value: number) => {
		"worklet";
		return reduceMotion ? withTiming(value, { duration: 0 }) : withSpring(value, spring.gentle);
	};
	const rest = () => {
		"worklet";
		scale.value = settle(MIN_ZOOM);
		x.value = settle(0);
		y.value = settle(0);
	};

	const pinch = Gesture.Pinch()
		.onStart(() => {
			startScale.value = scale.value;
			startX.value = x.value;
			startY.value = y.value;
		})
		.onUpdate((event) => {
			const next = clamp(startScale.value * event.scale, MIN_ZOOM * UNDERSHOOT, MAX_ZOOM);
			const factor = next / startScale.value;
			scale.value = next;
			x.value = zoomTranslation(startX.value, event.focalX - width / 2, factor);
			y.value = zoomTranslation(startY.value, event.focalY - height / 2, factor);
		})
		.onEnd(() => {
			const next = clamp(scale.value, MIN_ZOOM, MAX_ZOOM);
			if (next === MIN_ZOOM) {
				rest();
				return;
			}
			scale.value = settle(next);
			x.value = settle(boundTranslation(x.value, width, next));
			y.value = settle(boundTranslation(y.value, height, next));
		});

	// One finger: moves a zoomed image, and drags an unzoomed one away. Two
	// fingers belong to the pinch, which keeps the point under them still.
	const pan = Gesture.Pan()
		.maxPointers(1)
		.onStart(() => {
			startX.value = x.value;
			startY.value = y.value;
		})
		.onUpdate((event) => {
			if (scale.value > MIN_ZOOM) {
				x.value = startX.value + event.translationX;
				y.value = startY.value + event.translationY;
			} else {
				drag.value = event.translationY;
			}
		})
		.onEnd((event) => {
			if (scale.value > MIN_ZOOM) {
				x.value = settle(boundTranslation(x.value, width, scale.value));
				y.value = settle(boundTranslation(y.value, height, scale.value));
				return;
			}
			if (shouldDismiss(drag.value, event.velocityY)) {
				const away = (drag.value || event.velocityY) < 0 ? -height : height;
				drag.value = withTiming(away, { duration: reduceMotion ? 0 : duration.slow }, (finished) => {
					if (finished) scheduleOnRN(onClose);
				});
				return;
			}
			drag.value = settle(0);
		});

	// In to where the finger landed, or back out to the whole image.
	const doubleTap = Gesture.Tap()
		.numberOfTaps(2)
		.onEnd((event, success) => {
			if (!success) return;
			if (scale.value > MIN_ZOOM) {
				rest();
				return;
			}
			scale.value = settle(DOUBLE_TAP_ZOOM);
			x.value = settle(boundTranslation(zoomTranslation(0, event.x - width / 2, DOUBLE_TAP_ZOOM), width, DOUBLE_TAP_ZOOM));
			y.value = settle(boundTranslation(zoomTranslation(0, event.y - height / 2, DOUBLE_TAP_ZOOM), height, DOUBLE_TAP_ZOOM));
		});

	const gesture = Gesture.Simultaneous(pinch, pan, doubleTap);
	const imageStyle = useAnimatedStyle(() => ({
		transform: [{ translateX: x.value }, { translateY: y.value + drag.value }, { scale: scale.value }],
	}));
	const backdropStyle = useAnimatedStyle(() => ({ opacity: backdropOpacity(drag.value, height) }));

	return (
		<View accessibilityViewIsModal onAccessibilityEscape={onClose} style={styles.fill}>
			<Animated.View pointerEvents="none" style={[StyleSheet.absoluteFill, { backgroundColor: t.bgBase }, backdropStyle]} />
			<GestureDetector gesture={gesture}>
				<View collapsable={false} style={styles.fill}>
					<Animated.View style={imageStyle}>
						<Image accessibilityLabel={name} accessibilityIgnoresInvertColors source={imageSource} resizeMode="contain" style={{ width, height }} />
					</Animated.View>
				</View>
			</GestureDetector>
			<View style={[styles.close, { top: insets.top + space.sm, right: Math.max(insets.right, space.md) }]}>
				<NativeHeaderButton icon="close" label="Close image" onPress={onClose} />
			</View>
		</View>
	);
}

const styles = StyleSheet.create({
	fill: { flex: 1 },
	close: { position: "absolute" },
});
