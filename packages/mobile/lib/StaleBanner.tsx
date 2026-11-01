import { InlineBanner } from "./InlineBanner";
import { describeLinkHealth, type LinkBannerCopy } from "./linkHealth";
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
 *
 * It also says when the phone's own connection is the problem: no network, a
 * link still recovering from a blip, or one that answers slowly. Those are
 * amber and never name the machine as down. `networkOnly` limits it to the
 * phone being offline, for screens that show several machines at once, where
 * one machine's poll is not the whole story.
 */
export function StaleBanner({ error = false, onRetry, networkOnly = false }: { error?: boolean; onRetry?: () => void; networkOnly?: boolean }) {
	const { getLastSyncAt, config, linkHealth, selectedHostName } = useApp();
	const { stale, ageMs } = useStaleness(getLastSyncAt);

	const host = config?.host?.trim();
	const link = describeLinkHealth(linkHealth, selectedHostName ?? host);
	if (networkOnly && linkHealth !== "phone-offline") return null;
	// A machine that answered with an error (a rejected password, a 5xx) keeps
	// its own red copy; "slow" is no explanation for that.
	const showLink = link !== null && !(error && linkHealth === "slow");

	// Fresh and healthy: say nothing. Silence is the correct state here, and it
	// keeps the caller free of a conditional.
	if (!stale && !error && !showLink) return null;

	// A reachability failure is worth more alarm than mere age: one means the
	// desktop is gone, the other can just be a laptop that slept. The phone's own
	// connection is amber: nothing is wrong with the machine.
	const red = error && !showLink;
	const age = staleAgeLabel(ageMs);
	const text = showLink
		? link.text
		: error
			? host
				? `Can't reach ${host} — showing data from ${age}`
				: `Can't reach your machine — showing data from ${age}`
			: `Showing data from ${age}`;
	// Retrying can't help while the phone itself has no network.
	const retry = linkHealth === "phone-offline" ? undefined : onRetry;

	return (
		<InlineBanner
			accessibilityRole="alert"
			accessibilityLabel={text}
			tone={red ? "danger" : "warning"}
			icon={showLink ? linkSymbols[link.icon] : error ? "offline" : "history"}
			title={text}
			action={retry ? "Retry" : undefined}
			actionLabel="Retry now"
			onPress={retry}
		/>
	);
}

/** The link's own glyphs, as the app's system symbols. */
const linkSymbols = {
	"wifi-off": "offline",
	"refresh-cw": "refresh",
	activity: { ios: "waveform.path.ecg", android: "monitor_heart" },
} as const satisfies Record<LinkBannerCopy["icon"], unknown>;
