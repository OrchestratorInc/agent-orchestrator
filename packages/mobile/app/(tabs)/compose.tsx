/**
 * The New task button's slot in the tab bar on iOS (see ./_layout.tsx). The tab
 * is disabled, so it is never selected: pressing it opens the spawn sheet
 * instead. It still needs a route of its own, and draws nothing.
 */
export default function ComposeTab() {
	return null;
}

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
