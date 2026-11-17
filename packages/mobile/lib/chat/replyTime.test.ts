import { expect, it } from "vitest";
import { formatReplyTime } from "./replyTime";

const now = new Date(2026, 10, 13, 18, 0);
const at = (...parts: [number, number, number, number, number]) => new Date(...parts).toISOString();
const clock = (iso: string) => new Date(iso).toLocaleTimeString("en-US", { hour: "numeric", minute: "2-digit" });

it("shows only the time for a reply written today", () => {
	const iso = at(2026, 10, 13, 16, 17);
	expect(formatReplyTime(iso, now, "en-US")).toBe(clock(iso));
});

it("names yesterday before the time", () => {
	const iso = at(2026, 10, 12, 9, 5);
	expect(formatReplyTime(iso, now, "en-US")).toBe(`Yesterday · ${clock(iso)}`);
});

it("shows the date for anything older, with the year only when it is not this one", () => {
	expect(formatReplyTime(at(2026, 9, 11, 9, 5), now, "en-US")).toBe("Oct 11");
	expect(formatReplyTime(at(2025, 9, 11, 9, 5), now, "en-US")).toBe("Oct 11, 2025");
});

it("draws nothing for a missing or unreadable time", () => {
	expect(formatReplyTime(undefined, now)).toBe("");
	expect(formatReplyTime("not a date", now)).toBe("");
});
