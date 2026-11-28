import { createElement } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

vi.mock("./config", () => ({}));

import type { ArtifactsBackend, ArtifactSource } from "./artifactsBackend";
import type { FilesBackend } from "./files/backend";
import { UnreachableError } from "./connectionError";
import { afterVerified, createLiveSlot, HELD_WAIT_MS, holdArtifactsBackend, holdFilesBackend, resolveWhenVerified, retryWhenVerified, sourceKey, useOpenWhenVerified, useRetryAfterVerified, useVerifiedFor, useVerifiedOnMount } from "./heldBackend";

// While the app reconnects, a machine's last address may answer as another
// device until the identity probe says otherwise. What is on screen stays, but
// nothing new may go out with the credential until the machine is verified.

const daemon = (host: string) => ({
	key: "machine-a",
	cloud: false,
	canChange: true,
	webSource: (sessionId: string, path: string) => ({ uri: `http://${host}/${sessionId}/${path}`, headers: { Authorization: "Bearer machine-password" } }),
	imageSource: (sessionId: string, path: string) => ({ uri: `http://${host}/${sessionId}/${path}`, headers: { Authorization: "Bearer machine-password" } }),
	streamSource: () => undefined,
	markdownImageSource: () => undefined,
}) as unknown as ArtifactsBackend;

let renderer: ReactTestRenderer | undefined;
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.useRealTimers();
});

describe("a held backend's verification", () => {
	it("says whether the connection is verified, and waits for it with no limit", async () => {
		vi.useFakeTimers();
		const slot = createLiveSlot<ArtifactsBackend>();
		slot.set(daemon("lan"));
		const held = holdArtifactsBackend(slot, slot.get()!, "machine-a");
		expect(held.verified?.()).toBe(true);
		slot.set(null);
		expect(held.verified?.()).toBe(false);
		let verified = false;
		void held.whenVerified!().then(() => { verified = true; });
		// Not a request: it does not give up the way a held call does.
		await vi.advanceTimersByTimeAsync(HELD_WAIT_MS * 4);
		expect(verified).toBe(false);
		slot.set(daemon("tunnel"));
		await vi.advanceTimersByTimeAsync(0);
		expect(verified).toBe(true);
	});

	it("is on the Files backend too, and on another branch's", () => {
		const slot = createLiveSlot<FilesBackend>();
		const files = { key: "k", cloud: false, git: { at: () => files } } as unknown as FilesBackend;
		slot.set(files);
		const held = holdFilesBackend(slot, files, "machine-a");
		slot.set(null);
		expect(held.verified?.()).toBe(false);
		expect(held.git!.at({ branch: "main" }).verified?.()).toBe(false);
	});

	it("runs a retry at once when verified, else once verified, unless cancelled", async () => {
		const slot = createLiveSlot<ArtifactsBackend>();
		slot.set(daemon("lan"));
		const held = holdArtifactsBackend(slot, slot.get()!, "machine-a");
		const reload = vi.fn();
		afterVerified(held, reload);
		expect(reload).toHaveBeenCalledTimes(1);
		slot.set(null);
		afterVerified(held, reload);
		const cancel = new AbortController();
		const cancelled = vi.fn();
		afterVerified(held, cancelled, cancel.signal);
		cancel.abort();
		await Promise.resolve();
		expect(reload).toHaveBeenCalledTimes(1);
		slot.set(daemon("tunnel"));
		await Promise.resolve();
		await Promise.resolve();
		expect(reload).toHaveBeenCalledTimes(2);
		expect(cancelled).not.toHaveBeenCalled();
	});
});

describe("useVerifiedOnMount", () => {
	function Picture({ backend }: { backend: ArtifactsBackend }) {
		const verified = useVerifiedOnMount(backend);
		const source: ArtifactSource | undefined = verified ? backend.imageSource("s", "shot.png") : undefined;
		return source ? createElement("Image", { source }) : createElement("Placeholder");
	}
	const images = (root: ReactTestRenderer) => root.root.findAll((node) => (node.type as unknown) === "Image");

	it("keeps a picture already on screen through a reconnect", async () => {
		const slot = createLiveSlot<ArtifactsBackend>();
		slot.set(daemon("lan"));
		const held = holdArtifactsBackend(slot, slot.get()!, "machine-a");
		await act(async () => { renderer = create(createElement(Picture, { backend: held })); });
		const shown = images(renderer!)[0]!.props.source;
		slot.set(null);
		await act(async () => { renderer!.update(createElement(Picture, { backend: held })); });
		expect(images(renderer!)[0]!.props.source).toBe(shown);
	});

	it("mounts a picture fresh only once the machine is verified, never with the old address's credential", async () => {
		const slot = createLiveSlot<ArtifactsBackend>();
		slot.set(daemon("lan"));
		const held = holdArtifactsBackend(slot, slot.get()!, "machine-a");
		// Shown once (so the held builder would repeat it), then the app is backgrounded.
		held.imageSource("s", "shot.png");
		slot.set(null);
		await act(async () => { renderer = create(createElement(Picture, { backend: held })); });
		expect(images(renderer!)).toHaveLength(0);
		await act(async () => { slot.set(daemon("tunnel")); });
		await act(async () => {});
		expect(images(renderer!)[0]!.props.source.uri).toBe("http://tunnel/s/shot.png");
	});

	it("does not wait for a backend that needs no verification (Cloud's)", async () => {
		const cloud = { ...daemon("signed"), cloud: true } as ArtifactsBackend;
		await act(async () => { renderer = create(createElement(Picture, { backend: cloud })); });
		expect(images(renderer!)).toHaveLength(1);
	});

	// A view that mounted while the machine's connection was away, whose backend
	// then becomes Cloud's in place, must not stay on its placeholder.
	it("becomes ready when its backend changes in place to one with no verification", async () => {
		const slot = createLiveSlot<ArtifactsBackend>();
		slot.set(daemon("lan"));
		const held = holdArtifactsBackend(slot, slot.get()!, "machine-a");
		slot.set(null);
		await act(async () => { renderer = create(createElement(Picture, { backend: held })); });
		expect(images(renderer!)).toHaveLength(0);
		const cloud = { ...daemon("signed"), cloud: true } as ArtifactsBackend;
		await act(async () => { renderer!.update(createElement(Picture, { backend: cloud })); });
		expect(images(renderer!)[0]!.props.source.uri).toBe("http://signed/s/shot.png");
	});
});

describe("useVerifiedFor", () => {
	// A card's picture: mounted off screen, it loads only once it comes into view.
	function Card({ backend, visible }: { backend: ArtifactsBackend; visible: boolean }) {
		const wanted = visible ? backend.imageSource("s", "shot.png") : undefined;
		const source = useVerifiedFor(backend, sourceKey(wanted)) ? wanted : undefined;
		return source ? createElement("Image", { source }) : createElement("Placeholder");
	}
	const images = (root: ReactTestRenderer) => root.root.findAll((node) => (node.type as unknown) === "Image");

	it("checks when the picture first loads, not when the card mounted", async () => {
		const slot = createLiveSlot<ArtifactsBackend>();
		slot.set(daemon("lan"));
		const held = holdArtifactsBackend(slot, slot.get()!, "machine-a");
		// Mounted off screen while connected; the builder has answered once, so it would repeat the LAN address.
		await act(async () => { renderer = create(createElement(Card, { backend: held, visible: false })); });
		held.imageSource("s", "shot.png");
		slot.set(null);
		await act(async () => { renderer!.update(createElement(Card, { backend: held, visible: true })); });
		expect(images(renderer!)).toHaveLength(0);
		await act(async () => { slot.set(daemon("tunnel")); });
		await act(async () => {});
		expect(images(renderer!)[0]!.props.source.uri).toBe("http://tunnel/s/shot.png");
	});

	it("keeps what it loaded through a reconnect, and waits for anything else", async () => {
		function File({ backend, path }: { backend: ArtifactsBackend; path: string }) {
			const wanted = backend.webSource("s", path);
			return useVerifiedFor(backend, sourceKey(wanted)) && wanted ? createElement("Web", { source: wanted }) : createElement("Placeholder");
		}
		const pages = (root: ReactTestRenderer) => root.root.findAll((node) => (node.type as unknown) === "Web");
		const slot = createLiveSlot<ArtifactsBackend>();
		slot.set(daemon("lan"));
		const held = holdArtifactsBackend(slot, slot.get()!, "machine-a");
		held.webSource("s", "b.html");
		await act(async () => { renderer = create(createElement(File, { backend: held, path: "a.html" })); });
		const shown = pages(renderer!)[0]!.props.source;
		slot.set(null);
		await act(async () => { renderer!.update(createElement(File, { backend: held, path: "a.html" })); });
		expect(pages(renderer!)[0]!.props.source).toBe(shown);
		// Another file, whose LAN address the builder still remembers, is a new load: it waits.
		await act(async () => { renderer!.update(createElement(File, { backend: held, path: "b.html" })); });
		expect(pages(renderer!)).toHaveLength(0);
		await act(async () => { slot.set(daemon("tunnel")); });
		await act(async () => {});
		expect(pages(renderer!)[0]!.props.source.uri).toBe("http://tunnel/s/b.html");
	});

	it("keeps the source still on screen through a re-render while unverified", async () => {
		function File({ backend, path }: { backend: ArtifactsBackend; path: string }) {
			const wanted = backend.webSource("s", path);
			return useVerifiedFor(backend, sourceKey(wanted)) && wanted ? createElement("Web", { source: wanted }) : createElement("Placeholder");
		}
		const pages = (root: ReactTestRenderer) => root.root.findAll((node) => (node.type as unknown) === "Web");
		const slot = createLiveSlot<ArtifactsBackend>();
		slot.set(daemon("lan"));
		const held = holdArtifactsBackend(slot, slot.get()!, "machine-a");
		await act(async () => { renderer = create(createElement(File, { backend: held, path: "a.html" })); });
		const shown = pages(renderer!)[0]!.props.source;
		slot.set(null);
		for (let i = 0; i < 3; i++) {
			await act(async () => { renderer!.update(createElement(File, { backend: held, path: "a.html" })); });
			expect(pages(renderer!)[0]!.props.source).toBe(shown);
		}
	});

	// A -> B -> A while unverified: A left the screen, so showing it again is a
	// new load with the old address's credential, and must wait.
	it("waits for a source shown earlier once another replaced it (A -> B -> A)", async () => {
		function File({ backend, path }: { backend: ArtifactsBackend; path: string }) {
			const wanted = backend.webSource("s", path);
			return useVerifiedFor(backend, sourceKey(wanted)) && wanted ? createElement("Web", { source: wanted }) : createElement("Placeholder");
		}
		const pages = (root: ReactTestRenderer) => root.root.findAll((node) => (node.type as unknown) === "Web");
		const slot = createLiveSlot<ArtifactsBackend>();
		slot.set(daemon("lan"));
		const held = holdArtifactsBackend(slot, slot.get()!, "machine-a");
		// B's LAN address is remembered by the builder too.
		held.webSource("s", "b.html");
		await act(async () => { renderer = create(createElement(File, { backend: held, path: "a.html" })); });
		expect(pages(renderer!)[0]!.props.source.uri).toBe("http://lan/s/a.html");
		slot.set(null);
		await act(async () => { renderer!.update(createElement(File, { backend: held, path: "b.html" })); });
		expect(pages(renderer!)).toHaveLength(0);
		await act(async () => { renderer!.update(createElement(File, { backend: held, path: "a.html" })); });
		expect(pages(renderer!)).toHaveLength(0);
		await act(async () => { slot.set(daemon("tunnel")); });
		await act(async () => {});
		expect(pages(renderer!)[0]!.props.source.uri).toBe("http://tunnel/s/a.html");
	});

	it("waits again once the source left the screen, even for the same key (A -> nothing -> A)", async () => {
		const slot = createLiveSlot<ArtifactsBackend>();
		slot.set(daemon("lan"));
		const held = holdArtifactsBackend(slot, slot.get()!, "machine-a");
		await act(async () => { renderer = create(createElement(Card, { backend: held, visible: true })); });
		expect(images(renderer!)).toHaveLength(1);
		slot.set(null);
		await act(async () => { renderer!.update(createElement(Card, { backend: held, visible: false })); });
		await act(async () => { renderer!.update(createElement(Card, { backend: held, visible: true })); });
		expect(images(renderer!)).toHaveLength(0);
	});

	it("waits when another backend takes over the same key", async () => {
		const first = createLiveSlot<ArtifactsBackend>();
		first.set(daemon("lan"));
		const a = holdArtifactsBackend(first, first.get()!, "machine-a");
		const second = createLiveSlot<ArtifactsBackend>();
		second.set(daemon("lan"));
		const b = holdArtifactsBackend(second, second.get()!, "machine-b");
		await act(async () => { renderer = create(createElement(Card, { backend: a, visible: true })); });
		b.imageSource("s", "shot.png");
		first.set(null);
		second.set(null);
		await act(async () => { renderer!.update(createElement(Card, { backend: b, visible: true })); });
		expect(images(renderer!)).toHaveLength(0);
	});

	it("loads nothing for a null key, and does not wait for Cloud's", async () => {
		const cloud = { ...daemon("signed"), cloud: true } as ArtifactsBackend;
		await act(async () => { renderer = create(createElement(Card, { backend: cloud, visible: false })); });
		expect(images(renderer!)).toHaveLength(0);
		await act(async () => { renderer!.update(createElement(Card, { backend: cloud, visible: true })); });
		expect(images(renderer!)).toHaveLength(1);
	});
});

describe("retryWhenVerified", () => {
	const files = (host: string, present = true) => ({
		key: "machine-a",
		cloud: false,
		imageSource: async (_sessionId: string, path: string) => present ? { uri: `http://${host}/${path}` } : undefined,
	}) as unknown as FilesBackend;

	it("reads an image side again once verified when it timed out waiting for the connection", async () => {
		vi.useFakeTimers();
		const slot = createLiveSlot<FilesBackend>();
		slot.set(files("lan"));
		const held = holdFilesBackend(slot, slot.get()!, "machine-a");
		slot.set(null);
		let result: unknown = "pending";
		void retryWhenVerified(held, (signal) => held.imageSource("s", "a.png", "after", "v1", {}, signal)).then((value) => { result = value; });
		// The held call gives up as unreachable; that is not a missing image.
		await vi.advanceTimersByTimeAsync(HELD_WAIT_MS + 1);
		expect(result).toBe("pending");
		slot.set(files("tunnel"));
		await vi.advanceTimersByTimeAsync(0);
		expect(result).toEqual({ uri: "http://tunnel/a.png" });
	});

	it("keeps a genuine absence as it is, without waiting", async () => {
		const slot = createLiveSlot<FilesBackend>();
		slot.set(files("lan", false));
		const held = holdFilesBackend(slot, slot.get()!, "machine-a");
		expect(await retryWhenVerified(held, (signal) => held.imageSource("s", "a.png", "before", "v1", {}, signal))).toBeUndefined();
	});

	it("rejects other failures, and one from an attempt made while verified, at once", async () => {
		const slot = createLiveSlot<FilesBackend>();
		slot.set(files("lan"));
		const held = holdFilesBackend(slot, slot.get()!, "machine-a");
		const load = vi.fn(async () => { throw new UnreachableError("timeout"); });
		await expect(retryWhenVerified(held, load)).rejects.toBeInstanceOf(UnreachableError);
		expect(load).toHaveBeenCalledTimes(1);
		slot.set(null);
		const broken = vi.fn(async () => { throw new Error("bad image"); });
		await expect(retryWhenVerified(held, broken)).rejects.toThrow("bad image");
		expect(broken).toHaveBeenCalledTimes(1);
	});

	it("stops waiting once cancelled", async () => {
		const slot = createLiveSlot<FilesBackend>();
		slot.set(files("lan"));
		const held = holdFilesBackend(slot, slot.get()!, "machine-a");
		slot.set(null);
		const cancel = new AbortController();
		const load = vi.fn(async () => { throw new UnreachableError("offline"); });
		const pending = retryWhenVerified(held, load, cancel.signal);
		await Promise.resolve();
		cancel.abort();
		await expect(pending).rejects.toThrow();
		slot.set(files("tunnel"));
		await Promise.resolve();
		expect(load).toHaveBeenCalledTimes(1);
	});
});

describe("resolveWhenVerified", () => {
	const files = (host: string) => ({
		key: "machine-a",
		cloud: false,
		imageSource: async (_sessionId: string, path: string) => ({ uri: `http://${host}/${path}`, headers: { Authorization: "Bearer machine-password" } }),
	}) as unknown as FilesBackend;

	it("resolves through the connection verified now, never the address the pane was drawn from", async () => {
		const slot = createLiveSlot<FilesBackend>();
		slot.set(files("lan"));
		const held = holdFilesBackend(slot, slot.get()!, "machine-a");
		slot.set(null);
		const opened = resolveWhenVerified(held, (signal) => held.imageSource("s", "a.png", "after", "v1", {}, signal));
		await Promise.resolve();
		slot.set(files("tunnel"));
		expect(await opened).toEqual({ uri: "http://tunnel/a.png", headers: { Authorization: "Bearer machine-password" } });
	});

	it("resolves nothing once cancelled, and sends nothing for it", async () => {
		const slot = createLiveSlot<FilesBackend>();
		slot.set(files("lan"));
		const held = holdFilesBackend(slot, slot.get()!, "machine-a");
		slot.set(null);
		const resolve = vi.fn(async () => ({ uri: "x" }));
		const cancel = new AbortController();
		const opened = resolveWhenVerified(held, resolve, cancel.signal);
		cancel.abort();
		slot.set(files("tunnel"));
		expect(await opened).toBeUndefined();
		expect(resolve).not.toHaveBeenCalled();
	});

	it("resolves at once with a verified connection or one with no verification", async () => {
		const resolve = vi.fn(async () => ({ uri: "data:image/png;base64,AA" }));
		expect(await resolveWhenVerified({}, resolve)).toEqual({ uri: "data:image/png;base64,AA" });
		expect(resolve).toHaveBeenCalledTimes(1);
	});
});

describe("useRetryAfterVerified", () => {
	let retry: (fn: () => void) => void = () => {};
	function RetryButton({ backend, target }: { backend: ArtifactsBackend; target: string }) {
		retry = useRetryAfterVerified(backend, target);
		return null;
	}
	const machineAway = () => {
		const slot = createLiveSlot<ArtifactsBackend>();
		slot.set(daemon("lan"));
		const held = holdArtifactsBackend(slot, slot.get()!, "machine-a");
		slot.set(null);
		return { slot, held };
	};
	const settle = () => act(async () => { await Promise.resolve(); await Promise.resolve(); });

	it("runs at once with a verified connection", async () => {
		const slot = createLiveSlot<ArtifactsBackend>();
		slot.set(daemon("lan"));
		const held = holdArtifactsBackend(slot, slot.get()!, "machine-a");
		await act(async () => { renderer = create(createElement(RetryButton, { backend: held, target: "s\na.html" })); });
		const reload = vi.fn();
		retry(reload);
		expect(reload).toHaveBeenCalledTimes(1);
	});

	it("waits for the connection, then runs for the file it was pressed for", async () => {
		const { slot, held } = machineAway();
		await act(async () => { renderer = create(createElement(RetryButton, { backend: held, target: "s\na.html" })); });
		const reload = vi.fn();
		retry(reload);
		await settle();
		expect(reload).not.toHaveBeenCalled();
		await act(async () => { slot.set(daemon("tunnel")); });
		await settle();
		expect(reload).toHaveBeenCalledTimes(1);
	});

	// Following a link from unlisted file A to B reuses the same view: A's
	// Retry must not reload B's page once the connection comes back.
	it("drops a waiting retry when the view moves to another file or machine", async () => {
		const { slot, held } = machineAway();
		await act(async () => { renderer = create(createElement(RetryButton, { backend: held, target: "s\na.html" })); });
		const forA = vi.fn();
		retry(forA);
		await act(async () => { renderer!.update(createElement(RetryButton, { backend: held, target: "s\nb.html" })); });
		const other = machineAway();
		const forB = vi.fn();
		retry(forB);
		await act(async () => { renderer!.update(createElement(RetryButton, { backend: other.held, target: "s\nb.html" })); });
		await act(async () => { slot.set(daemon("tunnel")); other.slot.set(daemon("tunnel")); });
		await settle();
		expect(forA).not.toHaveBeenCalled();
		expect(forB).not.toHaveBeenCalled();
	});

	it("keeps only the latest press, and drops it on unmount", async () => {
		const { slot, held } = machineAway();
		await act(async () => { renderer = create(createElement(RetryButton, { backend: held, target: "s\na.html" })); });
		const first = vi.fn();
		const second = vi.fn();
		retry(first);
		retry(second);
		await act(async () => { slot.set(daemon("tunnel")); });
		await settle();
		expect(first).not.toHaveBeenCalled();
		expect(second).toHaveBeenCalledTimes(1);
		slot.set(null);
		const afterUnmount = vi.fn();
		retry(afterUnmount);
		act(() => renderer!.unmount());
		renderer = undefined;
		slot.set(daemon("lan"));
		await settle();
		expect(afterUnmount).not.toHaveBeenCalled();
	});
});

describe("useOpenWhenVerified", () => {
	type Opener = ReturnType<typeof useOpenWhenVerified<ArtifactSource>>;
	let opener: Opener;
	function Viewer({ backend }: { backend: ArtifactsBackend }) {
		opener = useOpenWhenVerified<ArtifactSource>(backend);
		return null;
	}
	const machineAway = () => {
		const slot = createLiveSlot<ArtifactsBackend>();
		slot.set(daemon("lan"));
		const held = holdArtifactsBackend(slot, slot.get()!, "machine-a");
		slot.set(null);
		return { slot, held };
	};
	const fresh = { uri: "http://tunnel/s/shot.png", headers: { Authorization: "Bearer machine-password" } };

	it("shows the open as pending while it waits for the connection, then opens the fresh source", async () => {
		vi.useFakeTimers();
		const { slot, held } = machineAway();
		await act(async () => { renderer = create(createElement(Viewer, { backend: held })); });
		const show = vi.fn();
		await act(async () => { opener.open("after", async () => fresh, show); });
		expect(opener.pending).toBe("after");
		expect(opener.failed).toBeUndefined();
		await act(async () => { slot.set(daemon("tunnel")); });
		await act(async () => { await vi.advanceTimersByTimeAsync(0); });
		expect(show).toHaveBeenCalledWith(fresh);
		expect(opener.pending).toBeUndefined();
		expect(opener.failed).toBeUndefined();
	});

	// A sustained outage: the tap must not do nothing for as long as it lasts.
	it("fails once the connection stays away too long, and a second tap tries again", async () => {
		vi.useFakeTimers();
		const { slot, held } = machineAway();
		await act(async () => { renderer = create(createElement(Viewer, { backend: held })); });
		const show = vi.fn();
		await act(async () => { opener.open("after", async () => fresh, show); });
		await act(async () => { await vi.advanceTimersByTimeAsync(HELD_WAIT_MS); });
		expect(opener.pending).toBeUndefined();
		expect(opener.failed).toBe("after");
		expect(show).not.toHaveBeenCalled();
		await act(async () => { opener.open("after", async () => fresh, show); });
		expect(opener.failed).toBeUndefined();
		expect(opener.pending).toBe("after");
		await act(async () => { slot.set(daemon("tunnel")); });
		await act(async () => { await vi.advanceTimersByTimeAsync(0); });
		expect(show).toHaveBeenCalledTimes(1);
	});

	it("fails when the verified connection cannot resolve the source", async () => {
		const slot = createLiveSlot<ArtifactsBackend>();
		slot.set(daemon("lan"));
		const held = holdArtifactsBackend(slot, slot.get()!, "machine-a");
		await act(async () => { renderer = create(createElement(Viewer, { backend: held })); });
		const show = vi.fn();
		await act(async () => { opener.open("before", async () => undefined, show); });
		expect(opener.failed).toBe("before");
		await act(async () => { opener.open("before", async () => { throw new Error("gone"); }, show); });
		expect(opener.failed).toBe("before");
		expect(show).not.toHaveBeenCalled();
	});

	it("drops an open replaced by another, or by leaving the screen, without a word", async () => {
		vi.useFakeTimers();
		const { slot, held } = machineAway();
		await act(async () => { renderer = create(createElement(Viewer, { backend: held })); });
		const first = vi.fn();
		const second = vi.fn();
		await act(async () => { opener.open("before", async () => fresh, first); });
		await act(async () => { opener.open("after", async () => fresh, second); });
		expect(opener.pending).toBe("after");
		await act(async () => { slot.set(daemon("tunnel")); });
		await act(async () => { await vi.advanceTimersByTimeAsync(0); });
		expect(first).not.toHaveBeenCalled();
		expect(second).toHaveBeenCalledTimes(1);
		expect(opener.failed).toBeUndefined();
		slot.set(null);
		const left = vi.fn();
		await act(async () => { opener.open("after", async () => fresh, left); });
		act(() => renderer!.unmount());
		renderer = undefined;
		await vi.advanceTimersByTimeAsync(HELD_WAIT_MS * 2);
		expect(left).not.toHaveBeenCalled();
	});
});
