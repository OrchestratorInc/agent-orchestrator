import { describe, expect, it } from "vitest";
import { createScanGate, RESCAN_GAP_MS } from "./scanGate";

// The camera reports the code in frame many times a second, so the pair screen
// has to decide which sightings start an attempt. Before this gate it simply
// stopped listening after the first one: a scan that failed while iOS's Local
// Network prompt was still up could never be scanned again, and the error left
// on screen told the user to do exactly that.
describe("deciding which camera sightings start a pairing attempt", () => {
	it("starts an attempt on the first sighting of a code", () => {
		const gate = createScanGate();
		expect(gate.sighting("A", 0)).toBe(true);
	});

	it("ignores every sighting while an attempt is in flight", () => {
		const gate = createScanGate();
		expect(gate.start()).toBe(true);
		expect(gate.sighting("A", 10)).toBe(false);
		expect(gate.sighting("B", 20)).toBe(false);
		expect(gate.start()).toBe(false);
	});

	it("stops for good once pairing succeeded", () => {
		const gate = createScanGate();
		gate.start();
		gate.succeeded();
		expect(gate.sighting("A", 10_000)).toBe(false);
		expect(gate.start()).toBe(false);
	});

	// Re-trying every frame while the failed code sits in view would flash the
	// error and buzz the phone in a loop.
	it("does not retry a failed code that stays in view", () => {
		const gate = createScanGate();
		gate.start();
		gate.failed("A", 1_000);
		for (let t = 1_100; t < 10_000; t += 100) expect(gate.sighting("A", t)).toBe(false);
	});

	it("retries a failed code once it is pointed at again", () => {
		const gate = createScanGate();
		gate.start();
		gate.failed("A", 1_000);
		expect(gate.sighting("A", 1_100)).toBe(false);
		// Out of frame for longer than the gap, then back.
		expect(gate.sighting("A", 1_100 + RESCAN_GAP_MS)).toBe(true);
	});

	// The camera's own gap counts too: no sighting for a while means the code
	// was not in view, whatever the reason.
	it("retries when the camera comes back after a pause", () => {
		const gate = createScanGate();
		gate.start();
		gate.failed("A", 1_000);
		expect(gate.sighting("A", 1_000 + RESCAN_GAP_MS)).toBe(true);
	});

	// The unreachable error tells the user to wait for the code to update with
	// remote access and scan it again; the updated code is a different string.
	it("starts an attempt for a different code right after a failure", () => {
		const gate = createScanGate();
		gate.start();
		gate.failed("A", 1_000);
		expect(gate.sighting("B", 1_050)).toBe(true);
	});

	it("forgets the failed code when the user cancels", () => {
		const gate = createScanGate();
		gate.start();
		gate.failed("A", 1_000);
		gate.start();
		gate.cancelled();
		expect(gate.sighting("A", 1_050)).toBe(true);
	});
});

// The Local Network prompt can still be up when the wait gives up and the error
// shows. Answering it then should connect, not leave the user to find Try again.
describe("retrying when the Local Network prompt is answered late", () => {
	it("returns the code once if it failed while waiting on local network access", () => {
		const gate = createScanGate();
		gate.start();
		gate.failed("A", 1_000, { awaitedLocalNetwork: true });
		expect(gate.promptAnswered()).toBe("A");
		expect(gate.promptAnswered()).toBeNull();
	});

	// A wrong password or a different machine is not fixed by the prompt.
	it("ignores failures that did not wait on local network access", () => {
		const gate = createScanGate();
		gate.start();
		gate.failed("A", 1_000);
		expect(gate.promptAnswered()).toBeNull();
	});

	it("does nothing while an attempt is in flight", () => {
		const gate = createScanGate();
		gate.start();
		gate.failed("A", 1_000, { awaitedLocalNetwork: true });
		gate.start();
		expect(gate.promptAnswered()).toBeNull();
	});

	it("does nothing after the user cancelled", () => {
		const gate = createScanGate();
		gate.start();
		gate.failed("A", 1_000, { awaitedLocalNetwork: true });
		gate.start();
		gate.cancelled();
		expect(gate.promptAnswered()).toBeNull();
	});
});

// An attempt can throw (storage, navigation) without reporting how it ended.
// The scanner must not stay claimed by it, or no scan works until the screen
// is reopened — the bug this gate exists to fix, by another route.
describe("releasing an attempt that ended without reporting", () => {
	it("frees the scanner when the attempt is still claimed", () => {
		const gate = createScanGate();
		gate.start();
		expect(gate.release()).toBe(true);
		expect(gate.sighting("A", 0)).toBe(true);
	});

	it("is a no-op once the attempt reported its end", () => {
		const gate = createScanGate();
		gate.start();
		gate.failed("A", 1_000);
		expect(gate.release()).toBe(false);
		gate.start();
		gate.succeeded();
		expect(gate.release()).toBe(false);
		expect(gate.sighting("B", 2_000)).toBe(false);
	});
});
