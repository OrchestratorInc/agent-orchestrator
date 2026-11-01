import { useLocalSearchParams, useRouter } from "expo-router";
import { ArtifactHistorySheet } from "../../lib/ArtifactHistorySheet";
import { backOr } from "../../lib/backNavigation";
import { HostScope } from "../../lib/store";

// Lets the sheet finish closing before the version opens behind it.
const AFTER_CLOSE_MS = 220;

export default function ArtifactHistoryRoute() {
	const { hostId } = useLocalSearchParams<{ hostId?: string }>();
	return hostId ? <HostScope key={hostId} hostId={hostId}><ArtifactHistoryContent /></HostScope> : <ArtifactHistoryContent />;
}

function ArtifactHistoryContent() {
	const router = useRouter();
	// `origin` is "gallery" when opened from a card, where the live file is not open yet.
	const { sessionId, path, name, hostId, origin } = useLocalSearchParams<{ sessionId: string; path: string; name?: string; hostId?: string; origin?: string }>();
	if (!sessionId || !path) return null;
	return (
		<ArtifactHistorySheet
			sessionId={sessionId}
			path={path}
			name={name || path.split("/").pop() || path}
			onSelect={(version) => {
				backOr(router);
				// The current version is the live file: already open behind the sheet,
				// unless the sheet came from the gallery.
				if (version === null && origin !== "gallery") return;
				setTimeout(() => {
					if (version === null) router.push({ pathname: "/artifact/[sessionId]", params: { sessionId, path, hostId } });
					else router.push({ pathname: "/artifact-version/[sessionId]", params: { sessionId, path, version: String(version), hostId } });
				}, AFTER_CLOSE_MS);
			}}
		/>
	);
}

export { SheetErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
