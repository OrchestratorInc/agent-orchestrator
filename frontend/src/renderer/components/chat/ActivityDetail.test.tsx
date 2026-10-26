import { act, fireEvent, render, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import type { ConversationActivity } from "../../types/conversation";
import { ActivityDetailProvider } from "../../hooks/useActivityDetail";
import { ActivityRow } from "./ChatTimelineItems";
import { TooltipProvider } from "../ui/tooltip";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../lib/host-clients", () => ({ clientForSessionHost: () => ({ GET: get }) }));

const cases: Array<[ConversationActivity["activityKind"], NonNullable<ConversationActivity["detail"]>]> = [
	["command", { command: "cat file", output: "complete command output\n".repeat(100) }],
	["file_change", { files: [{ path: "file.ts", status: "modified", additions: 1, deletions: 1, patch: "@@ -1 +1 @@\n-old\n+new\n" }] }],
	["mcp_tool", { toolName: "read", arguments: { path: "file" }, content: [{ type: "text", text: "complete tool output\n".repeat(100) }] }],
	["reasoning", { text: "Complete reasoning paragraph. ".repeat(100) }],
];

it.each(cases)("%s expands to identical text with compact and legacy pages", async (kind, detail) => {
	const full: ConversationActivity = {
		kind: "activity", id: `row-${kind}`, sequence: 2, revision: 1,
		activityKind: kind, status: "completed", summary: "Read file",
		detail, createdAt: "2026-10-26T00:00:00Z",
	};
	get.mockReset();
	get.mockResolvedValue({ data: full });
	function mount(activity: ConversationActivity) {
		return render(<TooltipProvider><ActivityDetailProvider sessionId="s1"><ActivityRow activity={activity} /></ActivityDetailProvider></TooltipProvider>);
	}
	const legacy = mount(full);
	if (kind !== "reasoning") fireEvent.click(legacy.container.querySelector("[aria-expanded='false']")!);
	const expected = legacy.container.textContent;
	legacy.unmount();
	const previewDetail: ConversationActivity["detail"] = kind === "file_change"
		? { files: [{ path: "file.ts", additions: 1, deletions: 1, patch: "preview" }] }
		: kind === "mcp_tool"
			? { toolName: "read", arguments: { path: "file" }, content: [{ type: "text", text: "preview" }] }
			: kind === "reasoning" ? { text: "preview" } : { command: "cat file", output: "preview" };
	const compact = mount({ ...full, detail: previewDetail, detailOmitted: true });
	if (kind !== "reasoning") {
		expect(get).not.toHaveBeenCalled();
		fireEvent.click(compact.container.querySelector("[aria-expanded='false']")!);
	}
	expect(compact.container.textContent).not.toContain("preview");
	await waitFor(() => expect(get).toHaveBeenCalledTimes(1));
	await waitFor(() => expect(compact.container.textContent).toBe(expected));
});

it("gates compact MCP image content and results until the matching full row arrives", async () => {
	const data = btoa(`<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"><title>${"full image ".repeat(20)}</title><rect width="1" height="1" /></svg>`);
	const image = { type: "image", mimeType: "image/svg+xml", data };
	const full: ConversationActivity = {
		kind: "activity", id: "image-row", sequence: 2, revision: 3,
		activityKind: "mcp_tool", status: "completed", summary: "Read image",
		detail: { toolName: "read", content: [image], result: { content: [image] } },
		createdAt: "2026-10-26T00:00:00Z",
	};
	function mount(activity: ConversationActivity) {
		return render(<TooltipProvider><ActivityDetailProvider sessionId="image-session"><ActivityRow activity={activity} /></ActivityDetailProvider></TooltipProvider>);
	}
	function body(container: HTMLElement) {
		return { text: container.textContent, images: Array.from(container.querySelectorAll("img"), (img) => img.getAttribute("src")) };
	}
	get.mockReset();
	const legacy = mount(full);
	fireEvent.click(legacy.container.querySelector("[aria-expanded='false']")!);
	const expected = body(legacy.container);
	legacy.unmount();
	const prefix = data.slice(0, 128);
	expect(data.length).toBeGreaterThan(128);
	let resolve!: (value: { data: ConversationActivity }) => void;
	get.mockReturnValue(new Promise<{ data: ConversationActivity }>((done) => { resolve = done; }));
	const compact = mount({
		...full, detailOmitted: true,
		detail: { toolName: "read", content: [{ ...image, data: prefix }], result: { content: [{ ...image, data: prefix }] } },
	});
	expect(get).not.toHaveBeenCalled();
	fireEvent.click(compact.container.querySelector("[aria-expanded='false']")!);
	await waitFor(() => expect(get).toHaveBeenCalledTimes(1));
	expect(compact.container.textContent).not.toContain(prefix);
	expect(compact.container.querySelector("img")).toBeNull();
	expect(compact.container.querySelector("pre")).toBeNull();
	await act(async () => { resolve({ data: full }); });
	await waitFor(() => expect(body(compact.container)).toEqual(expected));
});
