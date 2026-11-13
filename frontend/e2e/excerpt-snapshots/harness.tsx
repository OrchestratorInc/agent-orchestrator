import { useState } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nextProvider } from "react-i18next";
import { appI18n } from "../../src/renderer/i18n";
import { TooltipProvider } from "../../src/renderer/components/ui/tooltip";
import { ChatWorkspace } from "../../src/renderer/components/chat/ChatWorkspace";
import { chatFixtureSettled } from "../../src/renderer/lib/chat-fixture";
import type { ChatDraftExcerptReference } from "../../src/renderer/lib/chat-drafts";
import type { ConversationSnapshot } from "../../src/renderer/types/conversation";
import "../../src/renderer/styles.css";

const navigation = new URLSearchParams(location.search).has("navigation");
const filenames = new URLSearchParams(location.search).has("filenames");
const diagram = "```mermaid\nflowchart LR; A[Start]-->B[End]\n```";
const filler = (section: string) => Array.from({ length: 30 }, (_, index) => `${section} paragraph ${index + 1}. This separates diagram blocks in a long source response.`).join("\n\n");
const source = filenames ? "Open `src/app.ts` here" : navigation ? `${filler("Before")}\n\n${diagram}\n\n${filler("Between")}\n\n${diagram}\n\n${filler("After")}` : "Before **bold text** and *italic text*.\n\n```ts\n\tconst total = a + b;\n\n\treturn total;\n```\n\nAfter the code." +
	(new URLSearchParams(location.search).has("diagram") ? "\n\n```mermaid\nflowchart LR; A[Start]-->B[End]\n```" : "");
const now = "2026-11-05T08:00:00Z";
const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
document.documentElement.dataset.theme = "dark";

function Review() {
	const [sent, setSent] = useState<ChatDraftExcerptReference[]>([]);
	const [snapshot, setSnapshot] = useState<ConversationSnapshot>({
		...chatFixtureSettled, sessionId: "captured-excerpt-review", conversationId: "review-conversation",
		turns: [{ id: "source-turn", state: "completed", requestedAt: now, completedAt: now }],
		items: [
			{ kind: "message", id: "question", sequence: 1, revision: 1, turnId: "source-turn", role: "user", origin: "human", text: "Show an example with formatting and code.", streaming: false, createdAt: now },
			{ kind: "message", id: "source", sequence: 2, revision: 1, turnId: "source-turn", role: "assistant", origin: "provider", text: source, streaming: false, createdAt: now },
		],
	});
	return <main className="app-shell-root flex h-screen flex-col bg-background text-foreground">
		<div className="flex shrink-0 items-center justify-between border-b border-border px-5 py-2 text-sm"><span>Captured excerpt review</span>{navigation ? <button onClick={() => setSnapshot(current => ({ ...current, items: current.items.map(item => item.kind === "message" && item.id === "source" ? { ...item, revision: 2 } : item) }))}>Change source revision</button> : null}<button onClick={() => setSnapshot(current => ({ ...current, items: current.items.filter(item => item.id !== "source") }))}>Remove source message</button></div>
		<ChatWorkspace hideHeader onOpenFile={() => {}} filePaths={filenames ? ["src/app.ts"] : undefined} onEditMessage={async () => {}} snapshot={snapshot} excerptsEnabled onSend={async (text, _attachments, _id, excerpts) => {
			setSent(excerpts ?? []);
			setSnapshot(current => ({ ...current, latestSequence: 3, items: [...current.items, {
				kind: "message", id: "sent", sequence: 3, revision: 1, role: "user", origin: "human", text, streaming: false, createdAt: now,
				content: excerpts?.map(ref => ({ type: "excerpt", text: ref.text, annotationId: ref.id, sourceConversationId: ref.conversationId, sourceMessageId: ref.messageId, sourceRevision: ref.revision, snapshot: ref.snapshot })),
			}] }));
		}} />
		<output data-testid="sent-payload" hidden>{JSON.stringify(sent)}</output>
	</main>;
}

createRoot(document.getElementById("root")!).render(<QueryClientProvider client={client}><I18nextProvider i18n={appI18n}><TooltipProvider><Review /></TooltipProvider></I18nextProvider></QueryClientProvider>);
