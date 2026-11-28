import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { SessionDeepLinkEvent } from "../../main/session-deep-link";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import { aoBridge } from "../lib/bridge";
import { useNavigateToSession } from "../lib/navigate-to-session";
import { resolveSessionLink } from "../lib/session-links";
import { useUiStore } from "../stores/ui-store";

const TOAST = { tone: "error", placement: "top-center", dismissible: true, dedupeKey: "session-deep-link:error" } as const;

/**
 * Opens ao://sessions/{project}/{session} links that came from outside the app.
 * The main process has already validated and canonicalised the URL; this only
 * waits for the workspace list, then navigates (a terminated session opens its
 * retained history) or says why the link cannot be opened. It never acts on the
 * session.
 */
export function SessionDeepLinkHost() {
	const { t } = useTranslation();
	const workspaces = useWorkspaceQuery({ includeCloud: false });
	const navigateToSession = useNavigateToSession();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const [url, setUrl] = useState<string>();
	const latest = useRef({ showGlobalToast, t });
	latest.current = { showGlobalToast, t };

	useEffect(() => {
		const receive = (link: SessionDeepLinkEvent | null) => {
			if (!link) return;
			if ("url" in link) setUrl(link.url);
			else latest.current.showGlobalToast(latest.current.t(link.error === "malformed" ? "sessionLink.malformed" : "sessionLink.unsupported"), undefined, TOAST);
		};
		const stop = aoBridge.sessionLinks.onOpen(receive);
		void aoBridge.sessionLinks.takePending().then(receive);
		return stop;
	}, []);

	const { isSuccess, isError, data } = workspaces;
	useEffect(() => {
		if (!url || (!isSuccess && !isError)) return;
		setUrl(undefined);
		const resolved = isSuccess ? resolveSessionLink(url, data ?? []) : undefined;
		if (resolved) navigateToSession(resolved.projectId, resolved.sessionId);
		else showGlobalToast(t(isSuccess ? "sessionLink.notFound" : "sessionLink.unverified"), undefined, TOAST);
	}, [url, isSuccess, isError, data, navigateToSession, showGlobalToast, t]);

	return null;
}
