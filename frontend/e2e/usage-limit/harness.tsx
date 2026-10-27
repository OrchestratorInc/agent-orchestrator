// Real chat components with deterministic provider state; no agent process runs.
import { useState } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nextProvider } from "react-i18next";
import { appI18n } from "../../src/renderer/i18n";
import { ChatWorkspace } from "../../src/renderer/components/chat/ChatWorkspace";
import { TooltipProvider } from "../../src/renderer/components/ui/tooltip";
import type { ConversationItem, ConversationMessage, ConversationSnapshot } from "../../src/renderer/types/conversation";
import "../../src/renderer/styles.css";

const params = new URLSearchParams(window.location.search);
const harness = params.get("harness") === "codex" ? "codex" : "claude-code";
const now = new Date().toISOString();
const reset = params.has("unknown") ? undefined : new Date(Date.now() + Number(params.get("delay") ?? 3_600_000)).toISOString();
const prompt: ConversationMessage = {
	kind: "message", id: "prompt", turnId: "limited", sequence: 1, revision: 0, role: "user", origin: "human",
	text: "Continue improving the session experience.", streaming: false, createdAt: now,
};
const initial: ConversationSnapshot = {
	conversationId: "usage-limit", sessionId: "usage-limit", harness, mode: "chat",
	controller: { state: "ready" }, latestSequence: 1, oldestSequence: 1, hasMoreBefore: false, settings: {},
	turns: [{
		id: "limited", providerTurnId: "provider-limited", hasHumanPrompt: true, state: "failed", requestedAt: now, completedAt: now,
		errorMessage: "You've hit your session limit", usageLimit: { resetsAt: reset, autoResume: params.has("armed") },
	}],
	items: [prompt],
};
if (params.has("paginated")) {
	initial.items = Array.from({ length: 200 }, (_, index): ConversationItem => ({
		kind: "activity", id: `activity-${index}`, turnId: "limited", sequence: index + 2, revision: 0,
		activityKind: "command", status: "completed", summary: `Inspect session item ${index + 1}`,
		detail: { command: "git status --short", output: "" }, createdAt: now,
	}));
	initial.oldestSequence = 2;
	initial.latestSequence = 201;
	initial.hasMoreBefore = true;
}

function RecoveryFixture() {
	const [snapshot, setSnapshot] = useState(initial);
	return <div className="flex h-full flex-col">
		<p role="note" className="border-b border-border bg-sidebar px-3 py-2 text-xs text-muted-foreground">
			UI-only simulation. No daemon runs here; auto-resume does not execute.
		</p>
		<div className="min-h-0 flex-1">
			<ChatWorkspace snapshot={snapshot} sessionTitle="Usage-limit recovery" hasOlder={snapshot.hasMoreBefore}
				onLoadOlder={() => setSnapshot((current) => ({ ...current, items: [prompt, ...current.items], oldestSequence: 1, hasMoreBefore: false }))}
				retryControl={{
				retry: (turnId) => setSnapshot((current) => ({
					...current, controller: { state: "busy" },
					turns: [...current.turns, { id: "resumed", retryOfTurnId: turnId, state: "running", requestedAt: new Date().toISOString() }],
				})),
				setAutoResume: (turnId, enabled) => setSnapshot((current) => ({
					...current, turns: current.turns.map((turn) => turn.id === turnId
						? { ...turn, usageLimit: { ...turn.usageLimit, autoResume: enabled } } : turn),
				})),
			}} />
		</div>
	</div>;
}

createRoot(document.getElementById("root")!).render(
	<QueryClientProvider client={new QueryClient()}>
		<I18nextProvider i18n={appI18n}><TooltipProvider><RecoveryFixture /></TooltipProvider></I18nextProvider>
	</QueryClientProvider>,
);
