import { useEffect, useState } from "react";
import { StyleSheet, Text, View, type TextStyle } from "react-native";
import Animated, { cancelAnimation, Easing, useAnimatedProps, useSharedValue, withRepeat, withTiming } from "react-native-reanimated";
import Svg, { Defs, LinearGradient, Mask, Rect, Stop, Text as SvgText } from "react-native-svg";
import { ultraGradient } from "./tokens";
import { useReducedMotion } from "./useReducedMotion";

/*
 * Text in desktop's Ultracode gradient (`.ultra-text`): blue to purple to
 * pink and back, a band twice the text's width sweeping across it every
 * 3.2s, still under Reduce Motion. React Native has no gradient fill for
 * text, so the word is an SVG mask over a gradient band whose x is animated
 * on the UI thread. A hidden copy of the text, in the same style, sizes it.
 */

const SWEEP_MS = 3200;

const AnimatedRect = Animated.createAnimatedComponent(Rect);

export function GradientText({ children, style, testID }: { children: string; style: TextStyle; testID?: string }) {
	const reduceMotion = useReducedMotion();
	const [size, setSize] = useState<{ width: number; height: number } | null>(null);
	// The band's left edge, from -width (its second half over the text) to 0.
	const sweep = useSharedValue(0);
	const width = size?.width ?? 0;
	useEffect(() => {
		if (!width) return;
		if (reduceMotion) {
			cancelAnimation(sweep);
			sweep.value = -width / 2;
			return;
		}
		sweep.value = -width;
		sweep.value = withRepeat(withTiming(0, { duration: SWEEP_MS, easing: Easing.linear }), -1, false);
		return () => cancelAnimation(sweep);
	}, [width, reduceMotion, sweep]);
	const band = useAnimatedProps(() => ({ x: sweep.value }));
	const flat = StyleSheet.flatten(style);
	const fontSize = flat.fontSize ?? 14;
	const lineHeight = flat.lineHeight ?? fontSize * 1.25;

	return (
		<View testID={testID}>
			{/* Sizes the gradient, and holds the text's place; never seen. */}
			<Text style={[style, styles.measure]} numberOfLines={1} onLayout={(event) => setSize({ width: event.nativeEvent.layout.width, height: event.nativeEvent.layout.height })}>
				{children}
			</Text>
			{size ? (
				<Svg width={size.width} height={size.height} style={StyleSheet.absoluteFill} pointerEvents="none">
					<Defs>
						<LinearGradient id="ultra-band" x1="0" y1="0" x2="1" y2="0">
							{ultraGradient.map((color, i) => <Stop key={i} offset={i / (ultraGradient.length - 1)} stopColor={color} />)}
						</LinearGradient>
						<Mask id="ultra-word">
							<SvgText
								x={0}
								// The baseline, as the text's own line box sets it.
								y={(lineHeight + fontSize * 0.7) / 2}
								fill="#ffffff"
								fontFamily={flat.fontFamily}
								fontSize={fontSize}
								fontWeight={flat.fontWeight}
								letterSpacing={flat.letterSpacing}
							>
								{children}
							</SvgText>
						</Mask>
					</Defs>
					<AnimatedRect animatedProps={band} y={0} width={size.width * 2} height={size.height} fill="url(#ultra-band)" mask="url(#ultra-word)" />
				</Svg>
			) : null}
		</View>
	);
}

const styles = StyleSheet.create({
	measure: { opacity: 0 },
});
