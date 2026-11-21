import { Sidebar } from "../../../lib/sidebar/Sidebar";

export { RouteErrorBoundary as ErrorBoundary } from "../../../lib/RouteErrorBoundary";

/**
 * Home: the desktop's left sidebar as the app's main view. Every page it opens
 * (a session, a project, Pull requests, Notifications, Settings, the Workers
 * board) is pushed over it and comes back to it, as a phone's list-first apps do.
 */
export default function HomeScreen() {
	return <Sidebar />;
}
