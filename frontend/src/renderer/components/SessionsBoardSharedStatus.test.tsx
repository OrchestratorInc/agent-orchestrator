import { describe, expect, it } from "vitest";
import type { BoardSessionPresentation } from "@aoagents/product-ui";
import { appI18n } from "../i18n";
import { withSharedWorkStatus } from "./SessionsBoardAdapters";

const card: BoardSessionPresentation = { id: "s1", provider: "codex", status: "review_pending", title: "Login flow", updatedAt: "2026-10-24T00:00:00Z" };
const t = appI18n.t.bind(appI18n);

describe("withSharedWorkStatus", () => {
	it("says who else is writing to the agent, in the working color", () => {
		const shown = withSharedWorkStatus(card, [
			{ userId: "me", name: "Vaibhaav", present: true, state: "idle" },
			{ userId: "v", name: "Vaanya", present: true, state: "working" },
		], "me", t);
		expect(shown.statusPresentation).toMatchObject({ label: "Vaanya working", className: "text-status-working" });
	});

	it("leaves the card alone while nobody else is writing, or when it is the person themselves", () => {
		expect(withSharedWorkStatus(card, [{ userId: "v", name: "Vaanya", present: true, state: "idle" }], "me", t)).toBe(card);
		expect(withSharedWorkStatus(card, [{ userId: "me", name: "Me", present: true, state: "working" }], "me", t)).toBe(card);
		expect(withSharedWorkStatus(card, undefined, "me", t)).toBe(card);
	});

	it("keeps a provisioning card's own line", () => {
		const provisioning = { ...card, statusPresentation: { className: "x", indicatorClassName: "y", label: "Starting" } };
		expect(withSharedWorkStatus(provisioning, [{ userId: "v", name: "Vaanya", present: true, state: "working" }], "me", t)).toBe(provisioning);
	});
});
