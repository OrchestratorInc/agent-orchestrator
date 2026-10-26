import { Stack, useRouter } from "expo-router";
import { useMemo } from "react";
import { toolbarIcon } from "./toolbarIcon";

/** What the badge prints; past this it stops growing. */
const MAX_BADGE = 99;

/**
 * The board tabs' trailing header items: Settings, and Notifications with its
 * unread count. Native bar-button items on iOS, Compose icon buttons on Android.
 *
 * Notifications come from a paired machine, so a board with none shows Settings
 * alone; pairing starts from there.
 *
 * Memoised on what it shows: the tabs re-render on every poll, and each new
 * toolbar element would register the header's options again.
 */
export function BoardHeaderItems({ unread, notifications = true }: { unread: number; notifications?: boolean }) {
	const router = useRouter();
	return useMemo(() => (
		<Stack.Toolbar placement="right">
			<Stack.Toolbar.Button icon={toolbarIcon("settings")} accessibilityLabel="Settings" onPress={() => router.push("/settings")} />
			{notifications ? (
				<Stack.Toolbar.Button
					icon={toolbarIcon("notifications")}
					// iOS draws bar-item badges only from iOS 26, so the count is in the label too.
					accessibilityLabel={unread > 0 ? `Notifications, ${unread} unread` : "Notifications"}
					onPress={() => router.navigate("/notifications")}
				>
					{unread > 0 ? <Stack.Toolbar.Badge>{unread > MAX_BADGE ? `${MAX_BADGE}+` : String(unread)}</Stack.Toolbar.Badge> : null}
				</Stack.Toolbar.Button>
			) : null}
		</Stack.Toolbar>
	), [notifications, router, unread]);
}
