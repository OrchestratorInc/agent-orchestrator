import { createRoot } from "react-dom/client";
import { flushSync } from "react-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nextProvider } from "react-i18next";
import { appI18n } from "../../src/renderer/i18n";
import { TooltipProvider } from "../../src/renderer/components/ui/tooltip";
import { Timeline } from "../../src/renderer/components/chat/ChatWorkspace";
import { chatFixtureLongHistory } from "../../src/renderer/lib/chat-fixture";
import { useUiStore } from "../../src/renderer/stores/ui-store";
import "../../src/renderer/styles.css";

const root = createRoot(document.getElementById("root")!);
const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
let snapshot = chatFixtureLongHistory(60);
let remoteHost = false;
const paragraph = "Synthetic streamed geometry keeps the reader in control. ".repeat(8);

function render() {
	flushSync(() => root.render(
		<QueryClientProvider client={client}><I18nextProvider i18n={appI18n}><TooltipProvider>
			<Timeline snapshot={snapshot} remoteHost={remoteHost} draftScope={{ sessionId: snapshot.sessionId, incarnation: "browser-scroll" }} annotationNavigationRef={{ current: null }} />
		</TooltipProvider></I18nextProvider></QueryClientProvider>,
	));
}

export const scrollHarness = {
	open(remote: boolean) {
		remoteHost = remote;
		snapshot = chatFixtureLongHistory(60);
		snapshot.items = snapshot.items.filter((item) => item.kind === "message").map((item) => item.kind === "message" ? {
			...item,
			text: item.role === "user" ? `Synthetic reader prompt ${item.turnId}` : Array.from({ length: 4 }, () => paragraph).join("\n\n"),
		} : item);
		useUiStore.setState({ inspectorSessions: { "ao-long": { isOpen: false, view: "summary" } } });
		render();
	},
	grow(index = 59) {
		snapshot = {
			...snapshot,
			latestSequence: snapshot.latestSequence + 1,
			items: snapshot.items.map((item) => item.kind === "message" && item.role === "assistant" && item.turnId === `turn-h${index}` ? {
				...item, text: item.text + "\n\n" + paragraph, revision: item.revision + 1, streaming: true,
			} : item),
		};
		render();
	},
	async stream(index = 59) {
		for (let chunk = 0; chunk < 12; chunk++) {
			this.grow(index);
			await new Promise((resolve) => setTimeout(resolve, 40));
		}
	},
};

declare global {
	interface Window { scrollHarness: typeof scrollHarness }
}
window.scrollHarness = scrollHarness;
if (new URLSearchParams(window.location.search).has("preview")) {
	scrollHarness.open(false);
	document.getElementById("preview-controls")!.hidden = false;
	document.getElementById("stream")!.addEventListener("click", () => void scrollHarness.stream());
}
