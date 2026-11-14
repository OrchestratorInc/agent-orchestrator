import { useState } from "react";
import { createRoot } from "react-dom/client";
import { I18nextProvider } from "react-i18next";
import { appI18n } from "../../src/renderer/i18n";
import { TooltipProvider } from "../../src/renderer/components/ui/tooltip";
import { ChatComposer } from "../../src/renderer/components/chat/ChatComposer";
import { QueuedMessageDock, type QueuedMessage } from "../../src/renderer/components/chat/QueuedMessageDock";
import { SharedComposerNotice } from "../../src/renderer/components/chat/SharedComposerNotice";
import type { CloudCpSessionLive } from "../../src/renderer/lib/cloud-cp";
import type { ComposerLeaseState } from "../../src/renderer/hooks/useComposerLease";
import "../../src/renderer/styles.css";

const holder = { userId: "alice", name: "Alice Example" };
const messages: QueuedMessage[] = ["helloo", "hi", "check the result"].map((text, index) => ({
	turnId: `queued-${index}`,
	message: { id: `message-${index}`, turnId: `queued-${index}`, kind: "message", role: "user", origin: "human", streaming: false, text, sequence: index, revision: 0, createdAt: "2026-10-27T00:00:00Z" },
}));
function Harness() {
	const [shared, setShared] = useState(true);
	const [waiting, setWaiting] = useState(false);
	const live: CloudCpSessionLive = {
		type: "session.live", you: "me", canTakeOver: false, canConfigure: false, chatSettings: {},
		viewers: [{userId: "me", name: "Me", role: "editor"}, ...(shared ? [{...holder, role: "member" as const}] : [])],
		composer: {holder: shared ? holder : null, epoch: 3, waitlist: []},
	};
	const lease: ComposerLeaseState = {
		shared, holding: false, locked: shared, holder: shared ? holder : undefined, canTakeOver: false,
		position: waiting ? 1 : undefined,
		remoteDraft: shared ? {holder, text: "Keep this live draft fully visible.", cursor: 34} : undefined,
		noteDraft: () => {}, requestTurn: () => setWaiting(true), leaveLine: () => setWaiting(false), takeOver: () => {}, sent: () => {},
	};
	return <main className="cursor-chat-surface mx-auto flex min-h-screen w-full max-w-3xl flex-col justify-end gap-6 p-6">
		<ChatComposer onSend={() => {}} disabled={shared} disabledPlaceholder={shared ? "" : undefined}
			queuedDock={<QueuedMessageDock messages={messages} onCancelQueuedTurn={async () => {}} />}
			notice={<SharedComposerNotice live={live} lease={lease} userId="me" />} />
		<button type="button" onClick={() => setShared(false)}>Show only me</button>
	</main>;
}
createRoot(document.getElementById("root")!).render(<I18nextProvider i18n={appI18n}><TooltipProvider><Harness /></TooltipProvider></I18nextProvider>);
