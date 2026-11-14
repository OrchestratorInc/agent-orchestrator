import { createRoot } from "react-dom/client";
import { ChatMarkdown } from "../../src/renderer/components/chat/ChatMarkdown";
import { useUiStore } from "../../src/renderer/stores/ui-store";
import "../../src/renderer/styles.css";

const root = createRoot(document.getElementById("reply")!);
useUiStore.setState({ renderMath: true });

export const mathCopyHarness = {
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
