import { InlineBanner } from "./InlineBanner";
import { staleAgeLabel } from "./screenState";
import { useApp } from "./store";
import { useStaleness } from "./useStaleness";

/**
 * Says out loud that the rows below are older than they look.
 *
 * The screens used to put their failure copy inside `ListEmptyComponent`, which
 * means it only ever appeared when there was nothing to show. A list that had
 * already loaded kept its rows and said nothing at all — so a board whose daemon
 * died an hour ago looked exactly like a live one, and people act on those rows.
 *
 * This component owns the clock rather than taking an age as a prop, and that is
 * deliberate: `useStaleness` re-renders whatever calls it every tick, so a screen
 * that called it would re-render its whole list on a timer. Here the cost is one
 * small component. It also self-gates to null when the data is current, so
 * screens can render it unconditionally.
 *
 * It draws the chat screen's banner, so the app has one banner language rather
 * than two that nearly match.
 */
export function StaleBanner({ error = false, onRetry }: { error?: boolean; onRetry?: () => void }) {
	const { getLastSyncAt, config } = useApp();
	const { stale, ageMs } = useStaleness(getLastSyncAt);

	// Fresh and healthy: say nothing. Silence is the correct state here, and it
	// keeps the caller free of a conditional.
	if (!stale && !error) return null;

	const host = config?.host?.trim();
	const age = staleAgeLabel(ageMs);
	const text = error
		? host
			? `Can't reach ${host} — showing data from ${age}`
			: `Can't reach your machine — showing data from ${age}`
		: `Showing data from ${age}`;

	return (
		<InlineBanner
			accessibilityRole="alert"
			accessibilityLabel={text}
			// A reachability failure is worth more alarm than mere age: one means the
			// desktop is gone, the other can just be a laptop that slept.
			tone={error ? "danger" : "warning"}
			icon={error ? "offline" : "history"}
			title={text}
			action={onRetry ? "Retry" : undefined}
			actionLabel="Retry now"
			onPress={onRetry}
		/>
	);
}
