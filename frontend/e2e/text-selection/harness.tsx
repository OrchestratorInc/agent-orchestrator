// Mounts read-only content surfaces under the app's global stylesheet, whose
// `body { user-select: none }` they must opt out of, so the selection spec can
// drag across them in a real browser. The Files tab has no dev:web fixture.
import { createRoot } from "react-dom/client";
import { MarkdownFileView } from "../../src/renderer/components/markdown/MarkdownFileView";
import "../../src/renderer/styles.css";

createRoot(document.getElementById("root")!).render(
	<>
		<div data-testid="markdown">
			<MarkdownFileView content={"# Notes\n\nRun ao session ls to list sessions.\n"} filePath="docs/notes.md" sessionId="demo" truncated={false} version={0} />
		</div>
		<p role="alert" className="text-xs text-error">Push rejected: branch demo/new-task-flake is protected.</p>
	</>,
);
