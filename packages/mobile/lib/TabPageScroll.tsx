import type { ReactNode } from "react";
import { ScrollView, StyleSheet } from "react-native";

/**
 * A board tab's content when it is not a list: its unpaired state, or the
 * spinner of a first load there or on Notifications.
 *
 * The page's bar can be translucent with the page laid out under it, so a
 * plain view would put its top under the header. A scroll view with automatic
 * insets starts below the bar, gives the bar's scroll edge something to follow,
 * and lets the content centre in the space that is left.
 */
export function TabPageScroll({ children }: { children: ReactNode }) {
	return (
		<ScrollView contentInsetAdjustmentBehavior="automatic" contentContainerStyle={styles.fill}>
			{children}
		</ScrollView>
	);
}

const styles = StyleSheet.create({
	fill: { flexGrow: 1 },
});
