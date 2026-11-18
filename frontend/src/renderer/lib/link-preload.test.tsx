import { createRef } from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AppLink } from "../components/AppLink";
import { linkPreviewQueryKey } from "../hooks/useLinkPreview";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { isPullRequestLink, observeLinkForPreload } from "./link-preload";
import { queryClient } from "./query-client";

const { getMock, readArtifact } = vi.hoisted(() => ({ getMock: vi.fn(), readArtifact: vi.fn() }));

vi.mock("./api-client", () => ({
	apiClient: { GET: getMock },
	apiErrorMessage: () => "request failed",
}));
vi.mock("../hooks/useWorkspaceQuery", () => ({ workspaceQueryKey: ["workspaces"] }));
vi.mock("../hooks/useSessionWorkspaceFiles", () => ({
	sessionArtifactFileQueryOptions: (sessionId: string, path: string, rawUrl: string) => ({
		queryKey: ["session-artifact-file", rawUrl, sessionId, path],
		queryFn: () => readArtifact(path),
	}),
}));

const observed = new Set<Element>();
let reportIntersection: (element: Element, isIntersecting: boolean) => void = () => undefined;

// The module keeps one observer for the page, so one fake serves every test.
vi.stubGlobal("IntersectionObserver", class {
	constructor(callback: (entries: Array<{ target: Element; isIntersecting: boolean }>) => void) {
		reportIntersection = (target, isIntersecting) => callback([{ target, isIntersecting }]);
	}
	observe(element: Element) { observed.add(element); }
	unobserve(element: Element) { observed.delete(element); }
	disconnect() { observed.clear(); }
});

const warmedImages: Array<{ src: string; crossOrigin: string | null; referrerPolicy: string }> = [];

vi.stubGlobal("Image", class {
	crossOrigin: string | null = null;
	referrerPolicy = "";
	set src(src: string) { warmedImages.push({ src, crossOrigin: this.crossOrigin, referrerPolicy: this.referrerPolicy }); }
});

const pullRequest = (number: number) => `https://github.com/acme/widgets/pull/${number}`;

function visibleLink(href: string) {
	const element = document.createElement("a");
	const stop = observeLinkForPreload(element, href);
	reportIntersection(element, true);
	return { element, stop };
}

const previewResponse = (url: string) => ({ data: { url, title: "A pull request" }, error: undefined });

afterEach(() => {
	getMock.mockReset();
	readArtifact.mockReset();
	warmedImages.length = 0;
	queryClient.clear();
	vi.useRealTimers();
});

describe("isPullRequestLink", () => {
	it.each([
		"https://github.com/acme/widgets/pull/12",
		"https://github.com/acme/widgets/pull/12/",
		"https://GitHub.com/acme/widgets/pull/12#issuecomment-1",
	])("accepts %s", (href) => expect(isPullRequestLink(href)).toBe(true));

	it.each([
		"http://github.com/acme/widgets/pull/12",
		"https://github.com/acme/widgets/pull/12?token=secret",
		"https://github.com/acme/widgets/pull/12/files",
		"https://github.com/acme/widgets/issues/12",
		"https://github.com.evil.example/acme/widgets/pull/12",
		"https://user:pass@github.com/acme/widgets/pull/12",
		"https://github.com:8443/acme/widgets/pull/12",
		"https://example.com/acme/widgets/pull/12",
		"not a url",
	])("rejects %s", (href) => expect(isPullRequestLink(href)).toBe(false));
});

describe("observeLinkForPreload", () => {
	it("does not watch or fetch arbitrary web links", async () => {
		vi.useFakeTimers();
		const element = document.createElement("a");
		expect(observeLinkForPreload(element, "https://example.com/reset?token=secret")).toBeUndefined();
		expect(observed.has(element)).toBe(false);
		await vi.advanceTimersByTimeAsync(1_000);
		expect(getMock).not.toHaveBeenCalled();
	});

	it("loads a pull request into the hover cache and warms its images like the card", async () => {
		vi.useFakeTimers();
		const url = pullRequest(1);
		getMock.mockResolvedValue({
			data: { url, title: "A pull request", imageUrl: "https://og.example/1.png", faviconUrl: "https://github.example/favicon.svg" },
			error: undefined,
		});
		visibleLink(url);

		await vi.advanceTimersByTimeAsync(249);
		expect(getMock).not.toHaveBeenCalled();
		await vi.advanceTimersByTimeAsync(1);
		await vi.advanceTimersByTimeAsync(0);

		expect(getMock).toHaveBeenCalledExactlyOnceWith("/api/v1/link-preview", {
			params: { query: { url } },
			signal: expect.any(AbortSignal),
		});
		expect(queryClient.getQueryData(linkPreviewQueryKey(url))).toMatchObject({ title: "A pull request" });
		expect(warmedImages).toEqual([
			{ src: "https://og.example/1.png", crossOrigin: null, referrerPolicy: "no-referrer" },
			{ src: "https://github.example/favicon.svg", crossOrigin: "anonymous", referrerPolicy: "no-referrer" },
		]);
	});

	it("does not fetch a link scrolled past before the dwell ends", async () => {
		vi.useFakeTimers();
		const { element } = visibleLink(pullRequest(2));
		await vi.advanceTimersByTimeAsync(100);
		reportIntersection(element, false);
		await vi.advanceTimersByTimeAsync(1_000);
		expect(getMock).not.toHaveBeenCalled();
	});

	it("stops watching an unmounted link and cancels its pending load", async () => {
		vi.useFakeTimers();
		const { element, stop } = visibleLink(pullRequest(3));
		stop?.();
		expect(observed.has(element)).toBe(false);
		await vi.advanceTimersByTimeAsync(1_000);
		expect(getMock).not.toHaveBeenCalled();
	});

	it("runs two loads at a time and drops queued links that leave the screen", async () => {
		vi.useFakeTimers();
		const pending: Array<() => void> = [];
		getMock.mockImplementation((_path: string, options: { params: { query: { url: string } } }) => new Promise((resolve) => {
			pending.push(() => resolve(previewResponse(options.params.query.url)));
		}));
		visibleLink(pullRequest(10));
		visibleLink(pullRequest(11));
		const queued = visibleLink(pullRequest(12));
		visibleLink(pullRequest(13));
		await vi.advanceTimersByTimeAsync(250);
		expect(getMock).toHaveBeenCalledTimes(2);

		reportIntersection(queued.element, false);
		while (pending.length) {
			pending.shift()?.();
			await vi.advanceTimersByTimeAsync(0);
		}

		const urls = getMock.mock.calls.map(([, options]) => options.params.query.url);
		expect(urls).toEqual([pullRequest(10), pullRequest(11), pullRequest(13)]);
	});

	it("loads a URL once across duplicate links and repeat visits", async () => {
		vi.useFakeTimers();
		const url = pullRequest(20);
		getMock.mockResolvedValue({ data: undefined, error: { message: "not found" } });
		const first = visibleLink(url);
		visibleLink(url);
		await vi.advanceTimersByTimeAsync(250);
		await vi.advanceTimersByTimeAsync(0);
		reportIntersection(first.element, false);
		reportIntersection(first.element, true);
		await vi.advanceTimersByTimeAsync(1_000);
		expect(getMock).toHaveBeenCalledTimes(1);
	});

	describe("artifact links", () => {
		const artifactFile = (path: string, overrides: Record<string, unknown> = {}) => ({
			kind: "file", name: path, path, rawUrl: `http://127.0.0.1/raw/${path}`, size: 10, updatedAt: "2026-11-09T00:00:00Z", ...overrides,
		});
		const seedWorkspace = (artifactFiles: unknown[], session: Record<string, unknown> = {}) => {
			queryClient.setQueryData(workspaceQueryKey, [{ id: "project", sessions: [{ id: "session", artifactFiles, ...session }] }]);
		};
		const artifactLink = (path: string) => `ao://sessions/project/session/artifacts/${path}`;

		async function settleVisibleLink(href: string) {
			const link = visibleLink(href);
			await vi.advanceTimersByTimeAsync(250);
			await vi.advanceTimersByTimeAsync(0);
			return link;
		}

		it("loads a text artifact into the query the card reads", async () => {
			vi.useFakeTimers();
			seedWorkspace([artifactFile("notes.md")]);
			readArtifact.mockResolvedValue({ content: "# Notes", binary: false });
			await settleVisibleLink(artifactLink("notes.md"));
			expect(readArtifact).toHaveBeenCalledExactlyOnceWith("notes.md");
			expect(queryClient.getQueryData(["session-artifact-file", "http://127.0.0.1/raw/notes.md", "session", "notes.md"])).toMatchObject({ content: "# Notes" });
		});

		it.each([
			["an image", [artifactFile("shot.png")], {}, "shot.png"],
			["an inline page", [artifactFile("page.html", { kind: "html", inlineUrl: "http://ao-inline-artifact.session.localhost/page.html" })], {}, "page.html"],
			["a remote session", [artifactFile("remote.md")], { hostId: "other-host" }, "remote.md"],
			["a cloud session", [artifactFile("cloud.md")], { cloud: { id: "c" } }, "cloud.md"],
			["an artifact with no raw URL", [artifactFile("bare.md", { rawUrl: undefined })], {}, "bare.md"],
		])("does not read %s", async (_label, files, session, path) => {
			vi.useFakeTimers();
			seedWorkspace(files, session);
			await settleVisibleLink(artifactLink(path));
			expect(readArtifact).not.toHaveBeenCalled();
		});

		it("tries again once an artifact reaches the workspace list", async () => {
			vi.useFakeTimers();
			seedWorkspace([]);
			const { element } = await settleVisibleLink(artifactLink("late.md"));
			expect(readArtifact).not.toHaveBeenCalled();

			seedWorkspace([artifactFile("late.md")]);
			readArtifact.mockResolvedValue({ content: "late", binary: false });
			reportIntersection(element, false);
			reportIntersection(element, true);
			await vi.advanceTimersByTimeAsync(250);
			await vi.advanceTimersByTimeAsync(0);

			expect(readArtifact).toHaveBeenCalledExactlyOnceWith("late.md");
		});
	});
});

describe("AppLink preloading", () => {
	it("opens a preloaded pull request card with its content and no second request", async () => {
		const url = pullRequest(30);
		getMock.mockResolvedValue(previewResponse(url));
		render(<AppLink href={url}>pull request</AppLink>);
		const link = screen.getByRole("link", { name: "pull request" });
		expect(observed.has(link)).toBe(true);

		reportIntersection(link, true);
		await waitFor(() => expect(queryClient.getQueryData(linkPreviewQueryKey(url))).toBeDefined());
		fireEvent.pointerOver(link, { pointerType: "mouse" });

		expect(await screen.findByTestId("link-preview-card")).toHaveTextContent("A pull request");
		expect(screen.queryByTestId("link-preview-loading")).not.toBeInTheDocument();
		expect(getMock).toHaveBeenCalledTimes(1);
	});

	it("forwards the caller's ref and stops watching on unmount", () => {
		const ref = createRef<HTMLAnchorElement>();
		const { unmount } = render(<AppLink ref={ref} href={pullRequest(31)}>pull request</AppLink>);
		const link = screen.getByRole("link", { name: "pull request" });
		expect(ref.current).toBe(link);
		expect(observed.has(link)).toBe(true);

		unmount();

		expect(ref.current).toBeNull();
		expect(observed.has(link)).toBe(false);
	});

	it("watches an artifact link only when it has a hover card to warm", () => {
		const href = "ao://sessions/project/session/artifacts/notes.md";
		const { rerender } = render(<AppLink href={href}>notes</AppLink>);
		expect(observed.has(screen.getByRole("link", { name: "notes" }))).toBe(false);

		rerender(<AppLink href={href} hoverPreview={() => null}>notes</AppLink>);

		expect(observed.has(screen.getByRole("link", { name: "notes" }))).toBe(true);
	});

	it("leaves generic web links out of the observer", () => {
		render(<AppLink href="https://example.com/docs">docs</AppLink>);
		expect(observed.has(screen.getByRole("link", { name: "docs" }))).toBe(false);
	});
});
