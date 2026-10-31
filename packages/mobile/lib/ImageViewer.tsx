import { Image, Modal, Platform, Pressable, ScrollView, StyleSheet, View, useWindowDimensions } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { haptics } from "./haptics";
import { Feather } from "./icons";

/** A local file, or a daemon URL that needs the connection's Bearer header. */
export type ImageViewerSource = { uri: string; headers?: Record<string, string> };

/** Full-screen image at its own aspect ratio; pinch-zoom where the platform scroll view supports it. */
export function ImageViewer({ visible, name, source, onClose }: { visible: boolean; name: string; source: ImageViewerSource; onClose(): void }) {
	const insets = useSafeAreaInsets();
	const { width, height } = useWindowDimensions();
	// RN Android forwards HTTP headers only for array sources.
	const imageSource = Platform.OS === "android" ? [source] : source;
	return <Modal visible={visible} transparent animationType="fade" statusBarTranslucent onRequestClose={onClose}>
		<View style={styles.viewer}>
			<ScrollView maximumZoomScale={4} minimumZoomScale={1} centerContent bouncesZoom showsHorizontalScrollIndicator={false} showsVerticalScrollIndicator={false}>
				<Image accessibilityLabel={name} accessibilityIgnoresInvertColors source={imageSource} resizeMode="contain" style={{ width, height }} />
			</ScrollView>
			<Pressable accessibilityRole="button" accessibilityLabel="Close image" hitSlop={10} onPress={() => { haptics.tap(); onClose(); }} style={[styles.close, { top: insets.top + 12 }]}>
				<Feather name="x" size={20} color="#fff" />
			</Pressable>
		</View>
	</Modal>;
}

// The viewer is a dark lightbox in both themes, so its colours are fixed.
const styles = StyleSheet.create({
	viewer: { flex: 1, backgroundColor: "rgba(0, 0, 0, 0.94)" },
	close: { position: "absolute", right: 16, width: 36, height: 36, borderRadius: 16, alignItems: "center", justifyContent: "center", backgroundColor: "rgba(255, 255, 255, 0.16)" },
});
