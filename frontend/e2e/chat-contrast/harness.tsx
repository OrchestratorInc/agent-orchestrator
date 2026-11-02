// Real composer components and production CSS, without the shell or daemon.
import { useState, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nextProvider } from "react-i18next";
import { appI18n } from "../../src/renderer/i18n";
import { TooltipProvider } from "../../src/renderer/components/ui/tooltip";
import { ChatComposer } from "../../src/renderer/components/chat/ChatComposer";
import { SideComposer } from "../../src/renderer/components/chat/IndependentSideChats";
import { HumanMessageEditor } from "../../src/renderer/components/chat/HumanMessageEditor";
import { emptySideChatDraft } from "../../src/renderer/components/chat/sideChatDraft";
import "../../src/renderer/styles.css";

const params = new URLSearchParams(location.search);
const theme = params.get("theme") === "light" ? "light" : "dark";
const translucent = params.get("transparency") === "true";
document.documentElement.dataset.theme = theme;
document.documentElement.dataset.windowTranslucent = String(translucent);
const noop = () => {};
const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });

function Sample({ name, children }: { name: string; children: ReactNode }) {
	return <section aria-label={name}><h2 className="mb-2 text-sm text-muted-foreground">{name}</h2>{children}</section>;
}

function SideSample({ running = false }: { running?: boolean }) {
	const [draft, setDraft] = useState({ ...emptySideChatDraft(), text: running ? "" : "A side-chat question" });
	return <SideComposer draft={draft} onChange={(text) => setDraft({ ...draft, text })}
		onSend={noop} onInterrupt={noop} onAttach={noop} files={[]} onRemoveFile={noop}
		ready running={running} sending={false} models={[]} skills={[]}
		side={{ id: "side", label: "Side chat", sessionId: "fixture", mainConversationId: "main", contextMode: "fork", state: "ready", hasWork: false, createdAt: "2026-10-21T00:00:00Z", updatedAt: "2026-10-21T00:00:00Z" }}
		onSettings={noop} onRemoveReference={noop} onRemoveAllReferences={noop} onCompact={noop} focusKey={0} />;
}

createRoot(document.getElementById("root")!).render(
	<QueryClientProvider client={client}><I18nextProvider i18n={appI18n}><TooltipProvider>
		<main className="app-shell-root min-h-screen p-6 text-foreground">
			<h1 className="mb-1 text-lg">Chat controls · {theme} · transparency {translucent ? "on" : "off"}</h1>
			<p className="mb-6 text-sm text-muted-foreground">Isolated production components; no Electron window or daemon.</p>
			<div className="grid grid-cols-2 gap-6">
				<Sample name="Send and selected text"><ChatComposer onSend={noop} draftSeed={{ id: "send", text: "Selected text stays readable" }} /></Sample>
				<Sample name="Stop"><ChatComposer onSend={noop} willQueue onInterrupt={noop} /></Sample>
				<Sample name="Continue"><ChatComposer onSend={noop} onContinue={noop} /></Sample>
				<Sample name="Side-chat send"><SideSample /></Sample>
				<Sample name="Side-chat stop"><SideSample running /></Sample>
				<Sample name="Message-edit send"><HumanMessageEditor text="An edited message" content={[]} pending={false} busy={false} onCancel={noop} onSend={noop} /></Sample>
			</div>
		</main>
	</TooltipProvider></I18nextProvider></QueryClientProvider>,
);
