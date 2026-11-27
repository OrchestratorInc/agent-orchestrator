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

describe("geometry", () => {
	let registry: RefRegistry;
	let refs: Record<string, { role: string; name: string }>;
	const refFor = (name: string) => Object.entries(refs).find(([, info]) => info.name === name)![0];
	const rect = (over: Partial<DOMRect>) =>
		({ left: 0, top: 0, width: 0, height: 0, right: 0, bottom: 0, x: 0, y: 0, toJSON: () => ({}), ...over }) as DOMRect;
	const visible = () => rect({ left: 10, top: 20, width: 100, height: 30, right: 110, bottom: 50 });

	beforeEach(() => {
		document.body.innerHTML = `<button id="save">Save</button>`;
		registry = new RefRegistry();
		refs = (handleContentRequest(document, registry, request("snapshot", { interactive: true })) as { refs: typeof refs }).refs;
	});

	it("recentres an element that is only partially inside the window", () => {
		const button = document.getElementById("save")!;
		const scrolled = vi.fn();
		button.scrollIntoView = scrolled;
		// jsdom's window is 1024x768: this rect sticks out of the bottom edge but
		// counts as inViewport, so its centre used to be clicked off-screen.
		vi.spyOn(button, "getBoundingClientRect").mockReturnValue(rect({ left: 10, top: 740, width: 100, height: 90, right: 110, bottom: 830 }));
		handleContentRequest(document, registry, request("geometry", { ref: refFor("Save") }));
		expect(scrolled).toHaveBeenCalledOnce();
	});

	it("names the covering element when the click point is verified", () => {
		const button = document.getElementById("save")!;
		vi.spyOn(button, "getBoundingClientRect").mockReturnValue(visible());
		const overlay = document.createElement("div");
		overlay.id = "consent-overlay";
		document.body.appendChild(overlay);
		(document as { elementFromPoint?: unknown }).elementFromPoint = () => overlay;
		try {
			const result = handleContentRequest(document, registry, request("geometry", { ref: refFor("Save"), verify: true })) as { coveredBy?: string };
			expect(result.coveredBy).toContain("#consent-overlay");
		} finally {
			delete (document as { elementFromPoint?: unknown }).elementFromPoint;
		}
	});

	it("accepts a hit on the element's own descendant", () => {
		const button = document.getElementById("save")!;
		button.innerHTML = "<span>Save</span>";
		vi.spyOn(button, "getBoundingClientRect").mockReturnValue(visible());
		(document as { elementFromPoint?: unknown }).elementFromPoint = () => button.querySelector("span");
		try {
			const result = handleContentRequest(document, registry, request("geometry", { ref: refFor("Save"), verify: true })) as { coveredBy?: string };
			expect(result.coveredBy).toBeUndefined();
		} finally {
			delete (document as { elementFromPoint?: unknown }).elementFromPoint;
		}
	});
});
