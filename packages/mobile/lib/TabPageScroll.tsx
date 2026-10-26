import type { ReactNode } from "react";
import { ScrollView, StyleSheet } from "react-native";

/**
 * A large-title page's content when it is not a list: a board tab's unpaired
 * state, or the spinner of a first load there or on Notifications.
 *
 * The page's large-title bar is translucent and the page is laid out under it,
 * so a plain view would put its top under the header. A scroll view with
 * automatic insets starts below the bar, gives the title something to collapse
 * with, and lets the content centre in the space that is left.
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
