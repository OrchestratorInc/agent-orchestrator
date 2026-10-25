import { beforeEach, describe, expect, it, vi } from "vitest";
import { handleContentRequest } from "./content";
import { RefRegistry } from "./snapshot";

function request(op: string, extra: Record<string, unknown> = {}) {
	return { aoBridge: 1 as const, op, ...extra };
}

describe("content operations", () => {
	let registry: RefRegistry;
	let refs: Record<string, { role: string; name: string }>;
	const refFor = (name: string) => Object.entries(refs).find(([, info]) => info.name === name)![0];

	beforeEach(() => {
		document.body.innerHTML = `
			<label for="email">Email</label><input id="email" value="old">
			<label for="country">Country</label>
			<select id="country"><option value="in">India</option><option value="jp">Japan</option></select>
			<label><input id="agree" type="checkbox"> Agree</label>
			<button id="save">Save</button>
			<label for="pw">Password</label><input id="pw" type="password" value="secret">
			<p id="status">Idle</p>`;
		registry = new RefRegistry();
		refs = (handleContentRequest(document, registry, request("snapshot", { interactive: true })) as { refs: typeof refs }).refs;
	});

	it("fills fields with input and change events", () => {
		const input = document.getElementById("email") as HTMLInputElement;
		const events: string[] = [];
		input.addEventListener("input", () => events.push("input"));
		input.addEventListener("change", () => events.push("change"));
		handleContentRequest(document, registry, request("fill", { ref: refFor("Email"), text: "new@example.com" }));
		expect(input.value).toBe("new@example.com");
		expect(events).toEqual(["input", "change"]);
	});

	it("types at the cursor", () => {
		const input = document.getElementById("email") as HTMLInputElement;
		input.setSelectionRange(3, 3);
		handleContentRequest(document, registry, request("type", { ref: refFor("Email"), text: "!" }));
		expect(input.value).toBe("old!");
	});

	it.each([["email", "me@example.test"], ["number", "123"]])("types into %s fields and emits input", (type, text) => {
		const input = document.getElementById("email") as HTMLInputElement;
		input.type = type;
		input.value = "";
		const changed = vi.fn();
		input.addEventListener("input", changed);
		handleContentRequest(document, registry, request("type", { ref: refFor("Email"), text }));
		expect(input.value).toBe(text);
		expect(changed).toHaveBeenCalledOnce();
	});

	it("selects options by value or label", () => {
		const select = document.getElementById("country") as HTMLSelectElement;
		expect(handleContentRequest(document, registry, request("select", { ref: refFor("Country"), value: "Japan" }))).toEqual({ value: "jp" });
		expect(select.value).toBe("jp");
		expect(() => handleContentRequest(document, registry, request("select", { ref: refFor("Country"), value: "Peru" }))).toThrow(
			expect.objectContaining({ code: "OPTION_NOT_FOUND" }),
		);
	});

	it("checks and unchecks idempotently", () => {
		const ref = refFor("Agree");
		expect(handleContentRequest(document, registry, request("check", { ref }))).toEqual({ checked: true });
		expect(handleContentRequest(document, registry, request("check", { ref }))).toEqual({ checked: true });
		expect(handleContentRequest(document, registry, request("uncheck", { ref }))).toEqual({ checked: false });
	});

	it("dispatches synthetic clicks", () => {
		let clicks = 0;
		document.getElementById("save")!.addEventListener("click", () => clicks++);
		handleContentRequest(document, registry, request("click", { ref: refFor("Save") }));
		expect(clicks).toBe(1);
	});

	it("reads text and values but never password values", () => {
		expect(handleContentRequest(document, registry, request("get", { property: "text" }))).toMatchObject({ value: expect.stringContaining("Idle") });
		expect(handleContentRequest(document, registry, request("get", { property: "value", ref: refFor("Email") }))).toEqual({ value: "old" });
		expect(() => handleContentRequest(document, registry, request("get", { property: "value", ref: refFor("Password") }))).toThrow(
			expect.objectContaining({ code: "INVALID_ARGUMENT" }),
		);
	});

	it("waits for text to appear", async () => {
		const pending = handleContentRequest(document, registry, request("wait", { kind: "text", value: "Saved", timeoutMs: 2_000 })) as Promise<unknown>;
		setTimeout(() => (document.getElementById("status")!.textContent = "Saved"), 20);
		await expect(pending).resolves.toEqual({ satisfied: true });
		await expect(
			handleContentRequest(document, registry, request("wait", { kind: "selector", value: "#missing", timeoutMs: 150 })) as Promise<unknown>,
		).resolves.toEqual({ satisfied: false });
	});

	it("waits for a selector to be visible, not merely attached", async () => {
		const box = (width: number, height: number) => ({ left: 0, top: 0, width, height, right: width, bottom: height, x: 0, y: 0, toJSON: () => ({}) }) as DOMRect;
		document.body.insertAdjacentHTML("beforeend", `<div id="panel" style="display:none">Ready</div><div id="ghost" style="visibility:hidden">Boo</div>`);
		const panel = document.getElementById("panel")!;
		const ghost = document.getElementById("ghost")!;
		// jsdom has no layout; give elements a box only when they are displayed.
		const rects = vi.spyOn(Element.prototype, "getBoundingClientRect").mockImplementation(function (this: Element) {
			return getComputedStyle(this).display === "none" ? box(0, 0) : box(100, 20);
		});
		try {
			const wait = (kind: string, value: string, timeoutMs = 150) =>
				handleContentRequest(document, registry, request("wait", { kind, value, timeoutMs })) as Promise<{ satisfied: boolean }>;
			// Attached but display:none, or visibility:hidden: not satisfied.
			await expect(wait("selector", "#panel")).resolves.toEqual({ satisfied: false });
			await expect(wait("selector", "#ghost")).resolves.toEqual({ satisfied: false });
			await expect(wait("selector-gone", "#panel")).resolves.toEqual({ satisfied: false });
			await expect(wait("selector-gone", "#ghost")).resolves.toEqual({ satisfied: false });

			const shown = wait("selector", "#panel", 2_000);
			setTimeout(() => (panel.style.display = "block"), 20);
			await expect(shown).resolves.toEqual({ satisfied: true });
			await expect(wait("selector-gone", "#panel")).resolves.toEqual({ satisfied: false });

			panel.style.display = "none";
			await expect(wait("selector-gone", "#panel")).resolves.toEqual({ satisfied: false });
			const removed = wait("selector-gone", "#panel", 2_000);
			setTimeout(() => panel.remove(), 20);
			await expect(removed).resolves.toEqual({ satisfied: true });
			ghost.style.visibility = "visible";
			await expect(wait("selector", "#ghost")).resolves.toEqual({ satisfied: true });
		} finally {
			rects.mockRestore();
		}
	});

	it("rejects stale refs and unknown operations", () => {
		document.getElementById("save")!.remove();
		expect(() => handleContentRequest(document, registry, request("click", { ref: refFor("Save") }))).toThrow(
			expect.objectContaining({ code: "STALE_REFERENCE" }),
		);
		expect(() => handleContentRequest(document, registry, request("eval"))).toThrow(expect.objectContaining({ code: "INVALID_ARGUMENT" }));
	});
});
