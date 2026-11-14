import { createElement } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

vi.mock("./config", () => ({}));

import type { ArtifactsBackend, ArtifactSource } from "./artifactsBackend";
import type { FilesBackend } from "./files/backend";
import { afterVerified, createLiveSlot, HELD_WAIT_MS, holdArtifactsBackend, holdFilesBackend, useVerifiedOnMount } from "./heldBackend";

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
});
