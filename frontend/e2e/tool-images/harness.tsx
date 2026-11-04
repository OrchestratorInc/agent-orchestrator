import { createRoot } from "react-dom/client";
import type { ConversationActivity } from "../../src/renderer/types/conversation";
import { ActivityRow } from "../../src/renderer/components/chat/ChatTimelineItems";
import { TooltipProvider } from "../../src/renderer/components/ui/tooltip";
import "../../src/renderer/i18n";
import "../../src/renderer/styles.css";

// Synthetic 32x32 checkerboard PNG, deliberately scaled only by the existing ChatImage component.
const data = "iVBORw0KGgoAAAANSUhEUgAAACAAAAAgCAYAAABzenr0AAAASUlEQVR4nO3OMQ0AMAwDwcApxGIKpnBJGcRDFXn5wePLF1nd086tcb99AAAAwA7YPlA9AAAA/IDtA9UDAADAD9g+UD0AAADsgAePf/6mCWRFdQAAAABJRU5ErkJggg==";
const image = { type: "image", data, mimeType: "image/png" };
const samples: unknown[] = [
	image,
	{ text: "Nested ACP image", content: { content: image } },
	{ text: "Source image", source: { data, media_type: "image/png" } },
	{ text: "Malformed image", content: { ...image, data: "iVBOR-invalid" } },
	{ type: "text", text: "Text-only result" },
	{ type: "text", text: "-old\n+new" },
	{ content: [image, { type: "resource", resource: { uri: "fixture:///report", text: "resource body" } }], structuredContent: { answer: 42, source: { data: "CSV source text", media_type: "text/plain" } } },
];
createRoot(document.getElementById("root")!).render(
	<TooltipProvider><main className="mx-auto max-w-2xl space-y-4 bg-background p-6 text-foreground">
		<h1>Tool image regression fixture</h1>
		{samples.map((content, index) => <section key={index} data-sample={index}>
			<ActivityRow activity={{
				kind: "activity", id: `synthetic-${index}`, sequence: index + 1, revision: 1,
				createdAt: "2026-10-26T00:00:00Z", activityKind: "mcp_tool", status: "completed",
				summary: `Read fixture ${index + 1}`,
				// Exercise historical provider wire shapes beyond the typed message-content intersection.
				detail: index === 6 ? { toolName: `Read fixture ${index + 1}`, result: content }
					: { toolName: `Read fixture ${index + 1}`, content: content as NonNullable<ConversationActivity["detail"]>["content"] },
			}} />
		</section>)}
	</main></TooltipProvider>,
);
