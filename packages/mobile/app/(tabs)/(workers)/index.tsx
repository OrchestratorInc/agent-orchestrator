import { useIsFocused } from "expo-router";
import { Sidebar } from "../../../lib/sidebar/Sidebar";
import { useSidebarEdgeSwipe } from "../../../lib/sidebar/sidebarContext";

export { RouteErrorBoundary as ErrorBoundary } from "../../../lib/RouteErrorBoundary";

/**
 * Home: the desktop sidebar's Sessions and Projects as the app's main view,
 * with New chat at the bottom right. The rest of the sidebar (Pull requests,
 * All workers, Notifications, Pinned, the account and Settings) is the side bar
 * that slides in from the left: from the header's button, or a swipe from the
 * left edge while home is in front. Every page it opens is pushed over home.
 */
export default function HomeScreen() {
	useSidebarEdgeSwipe(useIsFocused());
	return <Sidebar part="home" />;
}
