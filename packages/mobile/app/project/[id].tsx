import { Redirect } from "expo-router";

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

/**
 * A project has no page of its own: home lists each project with its
 * orchestrator and workers, as the desktop's sidebar does. The route stays so
 * an older link or notification that names a project still opens the app,
 * at home.
 */
export default function ProjectRoute() {
	return <Redirect href="/" />;
}
