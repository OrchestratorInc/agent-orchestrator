// Real chat components with deterministic provider state; no agent process runs.
import { useState } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nextProvider } from "react-i18next";
import { appI18n } from "../../src/renderer/i18n";
import { ChatWorkspace } from "../../src/renderer/components/chat/ChatWorkspace";
import { TooltipProvider } from "../../src/renderer/components/ui/tooltip";
import type { ConversationSnapshot } from "../../src/renderer/types/conversation";
import "../../src/renderer/styles.css";

const params = new URLSearchParams(window.location.search);
const now = new Date().toISOString();
const reset = params.has("unknown") ? undefined : new Date(Date.now() + Number(params.get("delay") ?? 3_600_000)).toISOString();
const initial: ConversationSnapshot = {
	conversationId: "usage-limit", sessionId: "usage-limit", harness: "claude-code", mode: "chat",
	controller: { state: "ready" }, latestSequence: 1, oldestSequence: 1, hasMoreBefore: false, settings: {},
	turns: [{
		id: "limited", providerTurnId: "provider-limited", state: "failed", requestedAt: now, completedAt: now,
		errorMessage: "You've hit your session limit", usageLimit: { resetsAt: reset, autoResume: false },
	}],
	items: [{
		kind: "message", id: "prompt", turnId: "limited", sequence: 1, revision: 0, role: "user", origin: "human",
		text: "Continue improving the session experience.", streaming: false, createdAt: now,
	}],
};

function RecoveryFixture() {
	const [snapshot, setSnapshot] = useState(initial);
	return <ChatWorkspace snapshot={snapshot} sessionTitle="Usage-limit recovery" retryControl={{
		retry: (turnId) => setSnapshot((current) => ({
			...current, controller: { state: "busy" },
			turns: [...current.turns, { id: "resumed", retryOfTurnId: turnId, state: "running", requestedAt: new Date().toISOString() }],
		})),
		setAutoResume: (turnId, enabled) => setSnapshot((current) => ({
			...current, turns: current.turns.map((turn) => turn.id === turnId
				? { ...turn, usageLimit: { ...turn.usageLimit, autoResume: enabled } } : turn),
		})),
	}} />;
}

createRoot(document.getElementById("root")!).render(
	<QueryClientProvider client={new QueryClient()}>
		<I18nextProvider i18n={appI18n}><TooltipProvider><RecoveryFixture /></TooltipProvider></I18nextProvider>
	</QueryClientProvider>,
);
