// Real conversation HTTP, production CDC invalidation, and daemon scheduling.
// The isolated backend fixture controls provider output; this page never retries on a timer.
import { useEffect, useSyncExternalStore } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider, useMutation, useQuery } from "@tanstack/react-query";
import { I18nextProvider } from "react-i18next";
import { appI18n } from "../../src/renderer/i18n";
import { ChatWorkspace } from "../../src/renderer/components/chat/ChatWorkspace";
import { Button } from "../../src/renderer/components/ui/button";
import { TooltipProvider } from "../../src/renderer/components/ui/tooltip";
import { useConversation, useConversationCommands } from "../../src/renderer/hooks/useConversation";
import { setApiBaseUrl } from "../../src/renderer/lib/api-client";
import { createEventTransport } from "../../src/renderer/lib/event-transport";
import { getEventsConnectionState, subscribeEventsConnection } from "../../src/renderer/lib/events-connection";
import "../../src/renderer/styles.css";

const params = new URLSearchParams(window.location.search);
function loopbackOrigin(raw: string): string {
	const url = new URL(raw);
	if (url.protocol !== "http:" || url.hostname !== "127.0.0.1" || !url.port || url.username || url.password) {
		throw new Error("The connected fixture requires an isolated loopback backend URL.");
	}
	return url.origin;
}
const fixtureBaseUrl = loopbackOrigin(params.get("fixtureBaseUrl") ?? "http://127.0.0.1");
const harness = params.get("harness") === "codex" ? "codex" : "claude-code";
const delayMs = Number(params.get("delay") ?? 10_000);
const queryClient = new QueryClient();

interface FixtureMetadata {
	baseUrl: string;
	sessions: Record<"claude-code" | "codex", string>;
}

function ConnectedRecoveryFixture({ sessionId }: { sessionId: string }) {
	const { snapshot, error, unavailable } = useConversation(sessionId);
	const commands = useConversationCommands(sessionId);
	const events = useSyncExternalStore(subscribeEventsConnection, getEventsConnectionState);
	useEffect(() => createEventTransport(queryClient).connect(), []);
	const start = useMutation({
		mutationFn: async () => {
			const response = await fetch(`${fixtureBaseUrl}/test/usage-limit/${encodeURIComponent(sessionId)}`, {
				method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ delayMs }),
			});
			if (!response.ok) throw new Error(`The controlled provider could not start (${response.status}).`);
			return response.json();
		},
	});
	const lastTurn = snapshot?.turns.at(-1);
	const testActive = lastTurn?.state === "running" || lastTurn?.state === "queued" || Boolean(lastTurn?.usageLimit);

	return <div className="flex h-full flex-col">
		<div className="flex flex-wrap items-center gap-2 border-b border-border bg-sidebar px-3 py-2 text-xs text-muted-foreground">
			<p role="note">Full daemon test with controlled provider processes. Scheduling, storage, HTTP, and live updates are real.</p>
			<span data-testid="connected-events-state">Events: {events}.</span>
			<Button size="sm" variant="secondary" disabled={!snapshot || events !== "connected" || start.isPending || testActive} onClick={() => start.mutate()}>
				{start.isPending ? "Starting quota test…" : "Start quota test"}
			</Button>
			{start.error ? <p role="alert">{start.error.message}</p> : null}
		</div>
		<div className="min-h-0 flex-1">
			{snapshot ? <ChatWorkspace snapshot={snapshot} sessionTitle="Connected usage-limit recovery" retryControl={commands.retryControl} />
				: <p role="status">{error ?? unavailable?.message ?? "Loading the isolated conversation…"}</p>}
		</div>
	</div>;
}

function FixtureLoader() {
	const fixture = useQuery({
		queryKey: ["usage-limit-test-fixture"],
		queryFn: async (): Promise<FixtureMetadata> => {
			const response = await fetch(`${fixtureBaseUrl}/test/usage-limit`);
			if (!response.ok) throw new Error(`The isolated daemon fixture is unavailable (${response.status}).`);
			const metadata: FixtureMetadata = await response.json();
			setApiBaseUrl(loopbackOrigin(metadata.baseUrl));
			return metadata;
		},
		retry: false,
	});
	return fixture.data ? <ConnectedRecoveryFixture sessionId={fixture.data.sessions[harness]} />
		: <p role="status">{fixture.error?.message ?? "Connecting to the isolated daemon…"}</p>;
}

createRoot(document.getElementById("root")!).render(
	<QueryClientProvider client={queryClient}>
		<I18nextProvider i18n={appI18n}><TooltipProvider><FixtureLoader /></TooltipProvider></I18nextProvider>
	</QueryClientProvider>,
);
