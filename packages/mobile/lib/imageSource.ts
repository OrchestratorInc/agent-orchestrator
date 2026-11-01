import { Platform } from "react-native";

/**
 * An authenticated image source in the shape this platform needs: React
 * Native 0.86's Android Image forwards HTTP headers only for an array source.
 */
export function imageSourceForPlatform<T extends { uri: string }>(source: T): T | T[] {
	return Platform.OS === "android" ? [source] : source;
}
