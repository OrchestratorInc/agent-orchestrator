// expo-router adds a sitemap route of its own unless the app has this file, and
// its page crashes on native (see NotFoundScreen). aomobile://_sitemap opens the
// not-found page instead.
export { RouteErrorBoundary as ErrorBoundary } from "../lib/RouteErrorBoundary";
export { NotFoundScreen as default } from "../lib/NotFoundScreen";
