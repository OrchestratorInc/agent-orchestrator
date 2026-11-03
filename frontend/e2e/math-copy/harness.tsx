// Mounts one assistant reply with math on, so the copy spec runs against the
// real renderer and a real browser's `innerText`, which jsdom lacks.
import { createRoot } from "react-dom/client";
import { ChatMarkdown } from "../../src/renderer/components/chat/ChatMarkdown";
import { useUiStore } from "../../src/renderer/stores/ui-store";
import "../../src/renderer/styles.css";

const root = createRoot(document.getElementById("reply")!);
useUiStore.setState({ renderMath: true });

export const mathCopyHarness = {
	// A streaming reply renders as one react-markdown root per block; a settled
	// one opened from history renders as a single root.
	render(text: string, streaming: boolean) {
		root.render(<ChatMarkdown key={String(streaming)} text={text} streaming={streaming} />);
	},
};

declare global {
	interface Window {
		mathCopyHarness: typeof mathCopyHarness;
	}
}
window.mathCopyHarness = mathCopyHarness;
