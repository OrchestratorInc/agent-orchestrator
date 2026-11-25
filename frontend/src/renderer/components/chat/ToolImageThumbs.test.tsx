import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { ConversationActivity } from "../../types/conversation";
import { ChatImageSourceProvider } from "./chat-image-source";
import { ToolImageThumbs, toolImagePath } from "./ToolImageThumbs";

const WT = "/Users/me/.ao/data/worktrees/shop/shop-1";
const IMAGE = [{ type: "content", content: { type: "image", mimeType: "image/png", bytes: 100 } }];

function read(path: string, overrides: Record<string, unknown> = {}): ConversationActivity {
	return {
		kind: "activity",
		id: `a-${path}`,
		sequence: 1,
		revision: 1,
		status: "completed",
		createdAt: "2026-11-25T00:00:00Z",
		activityKind: "mcp_tool",
		summary: `Read ${path}`,
		detail: { protocol: "acp", toolKind: "read", input: { file_path: path }, content: IMAGE, ...overrides },
	} as unknown as ConversationActivity;
}

describe("toolImagePath", () => {
	it("names an image a step read inside the workspace, relative to it", () => {
		expect(toolImagePath(read(`${WT}/.ao/attachments/shot.png`))).toBe(".ao/attachments/shot.png");
		expect(toolImagePath(read("docs/pricing.jpg"))).toBe("docs/pricing.jpg");
		expect(toolImagePath(read("/repo/app/logo.svg", { cwd: "/repo" }))).toBe("app/logo.svg");
	});

	it("names nothing for a text read, a file outside the workspace, or an unfinished step", () => {
		expect(toolImagePath(read(`${WT}/a.png`, { content: [{ type: "content", content: { type: "text", text: "x" } }] }))).toBeUndefined();
		expect(toolImagePath(read("/tmp/elsewhere.png"))).toBeUndefined();
		expect(toolImagePath({ ...read(`${WT}/a.png`), status: "running" } as ConversationActivity)).toBeUndefined();
	});
});

describe("ToolImageThumbs", () => {
	it("draws each image once, at most three, from the workspace's image route", () => {
		const activities = ["a.png", "b.png", "a.png", "c.png", "d.png"].map((name) => read(`${WT}/${name}`));
		const { container } = render(
			<ChatImageSourceProvider sessionId="shop-1" assetBaseUrl="http://daemon.test">
				<ToolImageThumbs activities={activities} />
			</ChatImageSourceProvider>,
		);
		const sources = [...container.querySelectorAll("img")].map((img) => new URL(img.getAttribute("src")!).searchParams.get("path"));
		expect(sources).toEqual(["a.png", "b.png", "c.png"]);
	});

	it("draws nothing outside a session's chat", () => {
		const { container } = render(<ToolImageThumbs activities={[read(`${WT}/a.png`)]} />);
		expect(container.querySelector("img")).toBeNull();
	});
});
