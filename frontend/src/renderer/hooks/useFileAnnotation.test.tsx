import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { useFileAnnotation } from "./useFileAnnotation";

const { postMock } = vi.hoisted(() => ({ postMock: vi.fn().mockResolvedValue({ data: {} }) }));
vi.mock("../lib/api-client", () => ({
	apiClient: { POST: postMock },
	apiErrorMessage: (_error: unknown, fallback: string) => fallback,
}));

describe("useFileAnnotation", () => {
	it("closes feedback when the same trigger is clicked again", () => {
		const { result } = renderHook(() => useFileAnnotation("sess-1"));
		const target = {
			path: "src/App.tsx",
			side: "new" as const,
			line: 12,
			scope: "unstaged",
			surface: "focused" as const,
		};

		act(() => result.current.begin(target));
		expect(result.current.targets).toEqual([target]);

		act(() => result.current.begin({ ...target }));
		expect(result.current.targets).toEqual([]);
	});

	it("submits feedback through the provided Cloud message sender", async () => {
		const sendMessage = vi.fn().mockResolvedValue(undefined);
		const { result } = renderHook(() => useFileAnnotation("cloud-session", { sendMessage }));

		const target = { path: "src/App.tsx", side: "file" as const, scope: "combined", surface: "focused" as const };
		act(() => result.current.begin(target));
		act(() => result.current.setDraft(target, "Please simplify this file."));
		await act(async () => result.current.submit());

		expect(sendMessage).toHaveBeenCalledOnce();
		expect(sendMessage).toHaveBeenCalledWith(expect.stringContaining("Please simplify this file."));
		expect(result.current.status).toBe("sent");
	});

	it("marks local inline feedback as user-authored", async () => {
		const { result } = renderHook(() => useFileAnnotation("sess-1"));

		const target = { path: "src/App.tsx", side: "new" as const, line: 12, scope: "unstaged", surface: "focused" as const };
		act(() => result.current.begin(target));
		await act(async () => result.current.submit(target, "Move this control closer to the heading."));

		expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/send", {
			params: { path: { sessionId: "sess-1" } },
			body: { message: expect.stringContaining("Move this control closer to the heading."), userAuthored: true },
		});
	});

	it("cancels an open composer when the file source changes", () => {
		const prSource = "PR #42 · files (https://example.test/acme/repo/pull/42)";
		const { result, rerender } = renderHook(({ source }) => useFileAnnotation("sess-1", { source }), { initialProps: { source: prSource } });
		const target = { path: "src/App.tsx", side: "new" as const, line: 12, scope: "unstaged", surface: "focused" as const };
		act(() => result.current.begin(target));
		act(() => result.current.setDraft(target, "stale feedback"));
		expect(result.current.targets[0]?.source).toBe(prSource);
		rerender({ source: "Workspace" });
		expect(result.current.targets).toEqual([]);
		expect(result.current.draftFor(target)).toBe("");
	});

	it("keeps written comments open across files and sends them in one message", async () => {
		const sendMessage = vi.fn().mockResolvedValue(undefined);
		const { result } = renderHook(() => useFileAnnotation("sess-1", { sendMessage }));
		const first = { path: "src/App.tsx", side: "new" as const, line: 12, scope: "combined", surface: "review" as const };
		const second = { path: "src/lib/api.ts", side: "old" as const, line: 4, scope: "combined", surface: "review" as const };
		const third = { path: "README.md", side: "file" as const, scope: "combined", surface: "review" as const };

		act(() => result.current.begin(first));
		act(() => result.current.setDraft(first, "Rename this prop."));
		act(() => result.current.begin(second));
		act(() => result.current.setDraft(second, "Keep the old guard."));
		act(() => result.current.begin(third));
		expect(result.current.targets.map((target) => target.path)).toEqual(["src/App.tsx", "src/lib/api.ts", "README.md"]);

		act(() => result.current.setDraft(third, "Mention the new flag."));
		await act(async () => result.current.submit());

		expect(sendMessage).toHaveBeenCalledOnce();
		const message = sendMessage.mock.calls[0][0] as string;
		expect(message).toContain("3 inline feedback comments");
		expect(message.indexOf("Rename this prop.")).toBeLessThan(message.indexOf("Keep the old guard."));
		expect(message.indexOf("Keep the old guard.")).toBeLessThan(message.indexOf("Mention the new flag."));
		expect(result.current.status).toBe("sent");
	});

	it("sends only its own comment from a box and keeps the others open", async () => {
		vi.useFakeTimers();
		const sendMessage = vi.fn().mockResolvedValue(undefined);
		const { result } = renderHook(() => useFileAnnotation("sess-1", { sendMessage }));
		const first = { path: "src/App.tsx", side: "new" as const, line: 12, surface: "review" as const };
		const second = { path: "src/lib/api.ts", side: "old" as const, line: 4, surface: "review" as const };
		act(() => result.current.begin(first));
		act(() => result.current.setDraft(first, "Rename this prop."));
		act(() => result.current.begin(second));

		await act(async () => result.current.submit(second, "Keep the old guard."));

		expect(sendMessage).toHaveBeenCalledOnce();
		expect(sendMessage.mock.calls[0][0]).toContain("Keep the old guard.");
		expect(sendMessage.mock.calls[0][0]).not.toContain("Rename this prop.");
		expect(result.current.statusFor(second)).toBe("sent");
		expect(result.current.statusFor(first)).toBe("idle");

		act(() => vi.advanceTimersByTime(1_200));
		expect(result.current.targets).toEqual([first]);
		expect(result.current.draftFor(first)).toBe("Rename this prop.");
		expect(result.current.status).toBe("idle");
		vi.useRealTimers();
	});

	it("closes a box left empty when another comment is started", () => {
		const { result } = renderHook(() => useFileAnnotation("sess-1"));
		const first = { path: "src/App.tsx", side: "new" as const, line: 12, surface: "focused" as const };
		const second = { path: "src/App.tsx", side: "new" as const, line: 30, surface: "focused" as const };

		act(() => result.current.begin(first));
		act(() => result.current.begin(second));

		expect(result.current.targets).toEqual([second]);
	});

	it("cancels one comment without discarding the others", () => {
		const { result } = renderHook(() => useFileAnnotation("sess-1"));
		const first = { path: "src/App.tsx", side: "new" as const, line: 12, surface: "focused" as const };
		const second = { path: "src/lib/api.ts", side: "new" as const, line: 30, surface: "focused" as const };
		act(() => result.current.begin(first));
		act(() => result.current.setDraft(first, "Keep me."));
		act(() => result.current.begin(second));

		act(() => result.current.cancel(second));

		expect(result.current.targets).toEqual([first]);
		expect(result.current.draftFor(first)).toBe("Keep me.");
	});

	it("skips empty boxes and keeps every comment for a retry when the send fails", async () => {
		const sendMessage = vi.fn().mockRejectedValueOnce(new Error("offline")).mockResolvedValue(undefined);
		const { result } = renderHook(() => useFileAnnotation("sess-1", { sendMessage }));
		const first = { path: "src/App.tsx", side: "new" as const, line: 12, surface: "focused" as const };
		const second = { path: "src/lib/api.ts", side: "new" as const, line: 30, surface: "focused" as const };
		act(() => result.current.begin(first));
		act(() => result.current.setDraft(first, "Only this one has text."));
		act(() => result.current.begin(second));

		await act(async () => result.current.submit());

		expect(result.current.status).toBe("error");
		expect(result.current.draftFor(first)).toBe("Only this one has text.");
		expect(sendMessage.mock.calls[0][0]).not.toContain("src/lib/api.ts");

		await act(async () => result.current.submit());
		expect(sendMessage).toHaveBeenCalledTimes(2);
		expect(result.current.status).toBe("sent");
	});
});
