import { openSignedInBrowser } from "./browserSignIn";
import type { CloudClient } from "./client";
import { changedInstallation } from "./newProject";
import type { CloudGitHubInstallation } from "./types";

export const INSTALL_POLL_MS = 2_500;
/** How long to wait for an install the person is still working through. */
export const INSTALL_WAIT_MS = 5 * 60_000;
/** How long to keep looking once they closed the browser, for a callback still landing. */
export const INSTALL_SETTLE_MS = 30_000;

type InstallClient = Pick<CloudClient, "startGitHubInstallation" | "listGitHubInstallations" | "syncGitHubInstallation">;

/**
 * Install the Orchestrator GitHub App, or give it more repositories, the way
 * the desktop does (CreateProjectFlow's connectGitHub): GitHub's page opens in
 * the browser that already holds the person's GitHub session (Safari's,
 * through an auth session, on iOS; Chrome's, through a Custom Tab, on
 * Android), so they never sign in to GitHub again. Meanwhile the phone polls
 * the control plane for the installation GitHub's callback records, and syncs
 * it so its repositories are listed.
 *
 * The callback ends on the control plane's own page ("Return to AO … You can
 * close this tab", sometimes with an offer to connect the person's other
 * GitHub accounts), not on a link back into the app, so the person closes
 * the browser themselves; the repositories are often listed by then.
 *
 * Resolves with the new or changed installation, or undefined when none came
 * before the browser closed or the wait ran out.
 */
export async function installGitHubApp(
	client: InstallClient,
	orgId: string,
	previous: readonly CloudGitHubInstallation[],
	alive: () => boolean = () => true,
): Promise<CloudGitHubInstallation | undefined> {
	const before = new Map(previous.map((item) => [item.id, item.updatedAt]));
	const { installationUrl } = await client.startGitHubInstallation(orgId);
	let closedAt: number | undefined;
	let failed: { cause: unknown } | undefined;
	void openSignedInBrowser(installationUrl).then(
		(how) => { if (how === "closed") closedAt = Date.now(); },
		(cause: unknown) => { failed = { cause }; },
	);
	const giveUpAt = Date.now() + INSTALL_WAIT_MS;
	while (alive()) {
		await new Promise((resolve) => setTimeout(resolve, INSTALL_POLL_MS));
		if (failed) throw failed.cause;
		if (!alive()) break;
		const { installations } = await client.listGitHubInstallations(orgId);
		const changed = changedInstallation(installations, before);
		if (changed) {
			// A new installation's repositories may still be syncing, and one that
			// just gained some can be marked ready before GitHub's webhook lands:
			// sync before the caller reads them.
			await client.syncGitHubInstallation(orgId, changed.id).catch(() => {});
			return changed;
		}
		const now = Date.now();
		if (now > giveUpAt || (closedAt !== undefined && now - closedAt > INSTALL_SETTLE_MS)) break;
	}
	return undefined;
}
