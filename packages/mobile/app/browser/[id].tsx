import { Redirect, useLocalSearchParams } from "expo-router";
import { legacyPreviewRoute } from "../../lib/preview/navigation";
import type { PreviewRouteParams } from "../../lib/preview/route";

/** Backward-compatible deep link; Preview now owns the session browser. */
export default function LegacyBrowserRoute() {
	const params = useLocalSearchParams<PreviewRouteParams>();
	return <Redirect href={legacyPreviewRoute(params) ?? "/"} />;
}

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
