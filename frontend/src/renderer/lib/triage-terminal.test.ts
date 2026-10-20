import { expect, it, vi } from "vitest";
import { TriageTerminalStream } from "./triage-terminal";
const encode = (text: string) => btoa(text);

it.each([true, false])("merges subscribed output with snapshot replay exactly once, attach first=%s", (attachFirst) => {
	const stream = new TriageTerminalStream();
	const write = vi.fn();
	stream.push(encode("cdef"), 6);
	if (attachFirst) stream.attach(write);
	stream.snapshot(encode("abcd"), 4);
	if (!attachFirst) stream.attach(write);
	stream.push(encode("efgh"), 8);
	stream.push(encode("efgh"), 8);
	expect(write.mock.calls.map(([bytes]) => new TextDecoder().decode(bytes)).join("")).toBe("abcdefgh");
});

it("bounds events queued before the snapshot and terminal attach", () => {
	const stream = new TriageTerminalStream();
	const write = vi.fn();
	stream.push(encode("x".repeat(70 * 1024)), 70 * 1024);
	stream.snapshot("", 0);
	stream.attach(write);
	expect(write.mock.calls[0][0]).toHaveLength(64 * 1024);
});
