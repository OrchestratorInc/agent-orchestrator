import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

/**
 * Selectors theme CSS may rely on; the agent guide
 * (backend/internal/skillassets/using-ao/commands/theme.md) lists them. Shared
 * theme files break silently when one leaves the source, so this fails first.
 * The second group are alternative names for the chat's parts that shared
 * theme files commonly target, so their CSS applies here too.
 */
const HOOKS: ReadonlyArray<{ selector: string; file: string; source: string }> = [
	{ selector: '[data-workspace-area="sidebar"]', file: "components/ui/sidebar.tsx", source: 'data-workspace-area="sidebar"' },
	{ selector: '[data-workspace-area="conversation"]', file: "components/chat/ChatWorkspace.tsx", source: 'data-workspace-area="conversation"' },
	{ selector: '[data-workspace-area="composer"]', file: "components/chat/ChatComposer.tsx", source: 'data-workspace-area="composer"' },
	{ selector: '[data-workspace-area="inspector"]', file: "components/SessionInspectorRail.tsx", source: 'data-workspace-area="inspector"' },
	{ selector: '[data-slot="sidebar-inner"]', file: "components/ui/sidebar.tsx", source: 'data-slot="sidebar-inner"' },
	{ selector: '[data-slot="sidebar-menu-button"]', file: "components/ui/sidebar.tsx", source: 'data-slot="sidebar-menu-button"' },
	{ selector: "[data-theme-frame]", file: "components/TitlebarNav.tsx", source: "data-theme-frame" },

	{ selector: ".messages-timeline-scroll", file: "components/chat/ChatWorkspace.tsx", source: "messages-timeline-scroll" },
	{ selector: '[data-message-role="user"]', file: "components/chat/ChatTimelineItems.tsx", source: 'data-message-role="user"' },
	{ selector: '[data-message-role="assistant"]', file: "components/chat/ChatTimelineItems.tsx", source: 'data-message-role="assistant"' },
	{ selector: "[data-user-message-bubble]", file: "components/chat/ChatTimelineItems.tsx", source: "data-user-message-bubble" },
	{ selector: ".chat-markdown", file: "components/chat/ChatMarkdown.tsx", source: "chat-md chat-markdown " },
	{ selector: ".chat-markdown-codeblock", file: "components/chat/ChatMarkdown.tsx", source: "chat-markdown-codeblock" },
	{ selector: "[data-chat-composer-form]", file: "components/chat/ChatWorkspace.tsx", source: "data-chat-composer-form" },
	{ selector: '[data-slot="composer-host"]', file: "components/chat/ChatComposer.tsx", source: 'data-slot="composer-host"' },
];

describe("theme CSS hooks", () => {
	it.each(HOOKS)("$selector is still in $file", ({ file, source }) => {
		expect(readFileSync(resolve(__dirname, "..", file), "utf8")).toContain(source);
	});

	it("are all named in the agent guide", () => {
		const guide = readFileSync(resolve(__dirname, "../../../../backend/internal/skillassets/using-ao/commands/theme.md"), "utf8");
		for (const { selector } of HOOKS) {
			if (selector === "[data-theme-frame]") continue;
			expect(guide, selector).toContain(selector);
		}
	});
});
