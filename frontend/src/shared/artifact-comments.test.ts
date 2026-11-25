import { describe, expect, it } from "vitest";
import type * as Contract from "../../../packages/cloud-client/src/types";
import {
	ARTIFACT_COMMENTS_CONNECT,
	artifactAnchorExcerpt,
	artifactAnchorTextHash,
	artifactCommentAuthorLabel,
	artifactCommentBodyProblem,
	artifactCommentMarkers,
	artifactCommentsEtag,
	formatArtifactCommentMessage,
	groupArtifactCommentThreads,
	isArtifactCommentAnchor,
	isArtifactCommentsConnectMessage,
	MAX_ARTIFACT_COMMENT_MESSAGE_LENGTH,
	parseArtifactCommentsEtag,
	parseBridgeToViewerMessage,
	parseViewerToBridgeMessage,
	type ArtifactComment,
	type ArtifactCommentAnchor,
	type ArtifactCommentAuthor,
	type ArtifactCommentEditInput,
	type ArtifactCommentsSnapshot,
	type ArtifactCommentThread,
	type ArtifactReplyInput,
	type ArtifactThreadCreateInput,
	type ArtifactThreadStatusInput,
} from "./artifact-comments";

// The shared types must stay exactly the contract's (contracts/cloud/openapi.yaml).
type Equal<A, B> = (<T>() => T extends A ? 1 : 2) extends <T>() => T extends B ? 1 : 2 ? true : false;
const contractPins: [
	Equal<ArtifactCommentAnchor, Contract.ArtifactShareCommentAnchor>,
	Equal<ArtifactCommentAuthor, Contract.ArtifactShareCommentAuthor>,
	Equal<ArtifactComment, Contract.ArtifactShareComment>,
	Equal<ArtifactCommentThread, Contract.ArtifactShareCommentThread>,
	Equal<ArtifactCommentsSnapshot, Contract.ArtifactShareCommentsSnapshot>,
	Equal<ArtifactThreadCreateInput, Contract.ArtifactShareThreadCreateInput>,
	Equal<ArtifactReplyInput, Contract.ArtifactShareReplyInput>,
	Equal<ArtifactThreadStatusInput, Contract.ArtifactShareThreadStatusInput>,
	Equal<ArtifactCommentEditInput, Contract.ArtifactShareCommentEditInput>,
] = [true, true, true, true, true, true, true, true, true];

const sam = { id: "00000000-0000-4000-8000-000000000001", name: "Sam" };
const aditi = { id: "00000000-0000-4000-8000-000000000002", name: "Aditi" };

function anchor(path: string, excerpt = ""): ArtifactCommentAnchor {
	return { path, tag: path.split(">").pop()!.split(":")[0], textHash: "9f2c00112233aabb", excerpt };
}

function comment(id: string, body: string, author: ArtifactCommentAuthor = sam, extra: Partial<ArtifactComment> = {}): ArtifactComment {
	return { id, threadId: "t", author, clientId: `c-${id}`, body, createdAt: "2026-11-23T10:00:00Z", editedAt: null, deleted: false, ...extra };
}

function thread(id: string, path: string, createdAt: string, status: "open" | "resolved" = "open", comments?: ArtifactComment[]): ArtifactCommentThread {
	return {
		id,
		version: 3,
		status,
		anchor: anchor(path),
		createdAt,
		updatedAt: createdAt,
		resolvedBy: null,
		resolvedAt: null,
		sentToAgentAt: null,
		sentToAgentCount: 0,
		comments: comments ?? [comment(`${id}-1`, "hi")],
	};
}

describe("artifact comment types", () => {
	it("pins the shared types to the Cloud contract", () => {
		expect(contractPins.every(Boolean)).toBe(true);
	});

	it("labels authors without ever needing an email", () => {
		expect(artifactCommentAuthorLabel(sam)).toBe("Sam");
		expect(artifactCommentAuthorLabel({ id: null, name: "Former user" })).toBe("Former user");
		expect(artifactCommentAuthorLabel({ id: sam.id, name: "  " })).toBe("Orchestrator user");
		expect(artifactCommentAuthorLabel({ id: null, name: "" })).toBe("Former user");
	});

	it("checks bodies the way the server does, counting code points", () => {
		expect(artifactCommentBodyProblem(" \n ")).toBe("empty");
		expect(artifactCommentBodyProblem("ok")).toBeNull();
		expect(artifactCommentBodyProblem("😀".repeat(5000))).toBeNull();
		expect(artifactCommentBodyProblem("a".repeat(5001))).toBe("too_long");
	});

	it("validates anchors", () => {
		expect(isArtifactCommentAnchor(anchor("body>main:1>h1:1", "Title"))).toBe(true);
		expect(isArtifactCommentAnchor(anchor("body"))).toBe(true);
		expect(isArtifactCommentAnchor({ ...anchor("body>main:1>h1:1"), path: "html>body" })).toBe(false);
		expect(isArtifactCommentAnchor({ ...anchor("body>main:0"), tag: "main" })).toBe(false);
		expect(isArtifactCommentAnchor({ ...anchor("body>p:1"), textHash: "XYZ" })).toBe(false);
		expect(isArtifactCommentAnchor({ ...anchor("body>p:1"), excerpt: "x".repeat(121) })).toBe(false);
	});

	it("round-trips the comments ETag", () => {
		expect(artifactCommentsEtag(41, 3)).toBe('"r41-v3"');
		expect(parseArtifactCommentsEtag('"r41-v3"')).toEqual({ revision: 41, version: 3 });
		expect(parseArtifactCommentsEtag('W/"r0-v1"')).toEqual({ revision: 0, version: 1 });
		expect(parseArtifactCommentsEtag('"abc"')).toBeNull();
	});

	it("hashes and excerpts normalised element text", async () => {
		// Test vector for the comment bridge: sha256("Cloudflare staging setup") → first 16 hex.
		await expect(artifactAnchorTextHash("  Cloudflare\n  staging   setup ")).resolves.toBe("cb58067869305738");
		// Only the first 200 code points count.
		await expect(artifactAnchorTextHash(`${"a".repeat(200)}b`)).resolves.toBe(await artifactAnchorTextHash("a".repeat(200)));
		expect(artifactAnchorExcerpt(` ${"x".repeat(130)} `)).toBe("x".repeat(120));
	});
});

describe("comment bridge protocol", () => {
	it("accepts the bridge's messages", () => {
		expect(parseBridgeToViewerMessage({ type: "ready", protocol: 1 })).toEqual({ type: "ready", protocol: 1 });
		expect(parseBridgeToViewerMessage({ type: "pick", anchor: anchor("body>main:1>h1:1", "Title") })).toEqual({
			type: "pick",
			anchor: anchor("body>main:1>h1:1", "Title"),
		});
		expect(parseBridgeToViewerMessage({ type: "marker", path: "body>p:2" })).toEqual({ type: "marker", path: "body>p:2" });
		expect(parseBridgeToViewerMessage({ type: "positions", positions: [{ path: "body>p:2", top: 120.5, found: true }] })).toEqual({
			type: "positions",
			positions: [{ path: "body>p:2", top: 120.5, found: true }],
		});
		expect(parseBridgeToViewerMessage({ type: "escape" })).toEqual({ type: "escape" });
	});

	it("drops forged or malformed messages from the page", () => {
		expect(parseBridgeToViewerMessage("pick")).toBeNull();
		expect(parseBridgeToViewerMessage({ type: "comment", body: "steal" })).toBeNull();
		expect(parseBridgeToViewerMessage({ type: "pick", anchor: { ...anchor("body>p:1"), path: "<img onerror>" } })).toBeNull();
		expect(parseBridgeToViewerMessage({ type: "positions", positions: [{ path: "body>p:1", top: Infinity, found: true }] })).toBeNull();
		expect(parseBridgeToViewerMessage({ type: "ready", protocol: "1" })).toBeNull();
	});

	it("accepts the selection rectangle, and a cleared selection", () => {
		expect(parseBridgeToViewerMessage({ type: "selection", path: "body>main:1>h1:1", top: 84.5, bottom: 132 })).toEqual({
			type: "selection",
			path: "body>main:1>h1:1",
			top: 84.5,
			bottom: 132,
		});
		expect(parseBridgeToViewerMessage({ type: "selection", path: null, top: 0, bottom: 0 })).toEqual({ type: "selection", path: null, top: 0, bottom: 0 });
		// Above the viewport is still a real position.
		expect(parseBridgeToViewerMessage({ type: "selection", path: "body>p:2", top: -40, bottom: -10 })).not.toBeNull();
	});

	it("drops a malformed selection", () => {
		expect(parseBridgeToViewerMessage({ type: "selection", path: "body>p:1", top: NaN, bottom: 10 })).toBeNull();
		expect(parseBridgeToViewerMessage({ type: "selection", path: "body>p:1", top: 0, bottom: Infinity })).toBeNull();
		expect(parseBridgeToViewerMessage({ type: "selection", path: "body>p:1", top: "0", bottom: 10 })).toBeNull();
		expect(parseBridgeToViewerMessage({ type: "selection", path: "<img onerror>", top: 0, bottom: 10 })).toBeNull();
		expect(parseBridgeToViewerMessage({ type: "selection", top: 0, bottom: 10 })).toBeNull();
		expect(parseBridgeToViewerMessage({ type: "selection", path: null, top: 0, bottom: 10, extra: "x" })).toEqual({ type: "selection", path: null, top: 0, bottom: 10 });
	});

	it("ignores message types it doesn't know, so a newer bridge can't break an older panel", () => {
		for (const type of ["hover", "selection-v2", "future", ""]) {
			expect(parseBridgeToViewerMessage({ type, path: "body>p:1", top: 1, bottom: 2 })).toBeNull();
		}
		expect(parseBridgeToViewerMessage({ path: "body>p:1" })).toBeNull();
		expect(parseViewerToBridgeMessage({ type: "selection", path: "body>p:1", top: 1, bottom: 2 })).toBeNull();
	});

	it("strips extra fields so nothing else rides along", () => {
		expect(parseBridgeToViewerMessage({ type: "escape", body: "x" })).toEqual({ type: "escape" });
	});

	it("parses panel messages on the bridge side", () => {
		expect(parseViewerToBridgeMessage({ type: "mode", on: true, kind: "markdown" })).toEqual({ type: "mode", on: true, kind: "markdown" });
		expect(parseViewerToBridgeMessage({ type: "anchors", anchors: [{ path: "body>p:1", textHash: "9f2c00112233aabb", count: 0 }] })).not.toBeNull();
		expect(parseViewerToBridgeMessage({ type: "anchors", anchors: [{ path: "body>p:1", textHash: "9f2c00112233aabb", count: -1 }] })).toBeNull();
		expect(parseViewerToBridgeMessage({ type: "select", path: "body>p:1" })).toEqual({ type: "select", path: "body>p:1" });
		expect(parseViewerToBridgeMessage({ type: "clear" })).toEqual({ type: "clear" });
		expect(parseViewerToBridgeMessage({ type: "theme", accent: "#5b5bd6", tint: "rgb(91 91 214 / 14%)" })).not.toBeNull();
		expect(parseViewerToBridgeMessage({ type: "theme", accent: "red;background:url(x)", tint: "#fff" })).toBeNull();
		expect(parseViewerToBridgeMessage({ type: "mode", on: true, kind: "pdf" })).toBeNull();
	});

	it("recognises the connect handshake", () => {
		expect(isArtifactCommentsConnectMessage({ type: ARTIFACT_COMMENTS_CONNECT, protocol: 1 })).toBe(true);
		expect(isArtifactCommentsConnectMessage({ type: "connect" })).toBe(false);
	});
});

describe("groupArtifactCommentThreads", () => {
	const threads = [
		thread("a", "body>p:3", "2026-11-23T10:05:00Z"),
		thread("b", "body>h1:1", "2026-11-23T10:02:00Z"),
		thread("c", "body>p:3", "2026-11-23T10:01:00Z"),
		thread("d", "body>p:3", "2026-11-23T10:00:00Z", "resolved"),
		thread("e", "body>table:1", "2026-11-23T10:03:00Z"),
		thread("f", "body>h1:1", "2026-11-23T09:00:00Z", "resolved"),
	];

	it("groups by element, orders groups by position and threads oldest first", () => {
		const groups = groupArtifactCommentThreads(threads, [
			{ path: "body>h1:1", top: 10, found: true },
			{ path: "body>p:3", top: 300, found: true },
			{ path: "body>table:1", top: 0, found: false },
		]);
		expect(groups.open.map((group) => [group.path, group.threads.map((t) => t.id), group.openCount])).toEqual([
			["body>h1:1", ["b"], 1],
			["body>p:3", ["c", "a"], 2],
		]);
		expect(groups.unanchored.map((group) => group.path)).toEqual(["body>table:1"]);
		expect(groups.resolved.map((group) => [group.path, group.threads.map((t) => t.id)])).toEqual([
			["body>h1:1", ["f"]],
			["body>p:3", ["d"]],
		]);
		expect(groups.openCount).toBe(4);
		expect(groups.resolvedCount).toBe(2);
	});

	it("falls back to oldest thread first before the bridge reports", () => {
		const groups = groupArtifactCommentThreads(threads);
		expect(groups.open.map((group) => group.path)).toEqual(["body>p:3", "body>h1:1", "body>table:1"]);
		expect(groups.unanchored).toEqual([]);
	});

	it("builds marker counts of open threads for every commented element", () => {
		expect(artifactCommentMarkers(threads)).toEqual([
			{ path: "body>h1:1", textHash: "9f2c00112233aabb", count: 1 },
			{ path: "body>p:3", textHash: "9f2c00112233aabb", count: 2 },
			{ path: "body>table:1", textHash: "9f2c00112233aabb", count: 1 },
		]);
	});
});

describe("formatArtifactCommentMessage", () => {
	const designThread: ArtifactCommentThread = {
		...thread("t", "body>main:1>h1:1", "2026-11-23T10:00:00Z", "open", [
			comment("1", "Can we say \"staging only\" in the title? People will skim this and assume it's production.", sam),
			comment("2", "Yes, will add.", aditi),
		]),
		anchor: { path: "body>main:1>h1:1", tag: "h1", textHash: "9f2c00112233aabb", excerpt: "Cloudflare staging setup for shared video streaming" },
	};

	it("matches the design's message", () => {
		expect(
			formatArtifactCommentMessage(
				designThread,
				{ path: "docs/cloudflare-staging-setup.html", version: 3 },
				"Rename the title and add a one-line staging warning under it.",
			),
		).toMatchInlineSnapshot(`
			"The owner forwarded a review comment on a shared artifact they published from this session.

			File: docs/cloudflare-staging-setup.html  (shared link version 3)
			Where: <h1> "Cloudflare staging setup for shared video streaming"  [selector: body>main:1>h1:1]

			Thread:
			- Sam: Can we say "staging only" in the title? People will skim this and assume it's production.
			- Aditi: Yes, will add.

			Owner's note: Rename the title and add a one-line staging warning under it.

			Update the file to address this, then tell the owner what changed. They will republish it."
		`);
	});

	it("leaves out an empty note and excerpt, and names deleted comments and authors", () => {
		const message = formatArtifactCommentMessage(
			{
				...designThread,
				anchor: { ...designThread.anchor, tag: "img", excerpt: "" },
				comments: [comment("1", "", sam, { deleted: true }), comment("2", "Still wrong", { id: null, name: "Former user" }), comment("3", "x", { id: sam.id, name: "Orchestrator user" })],
			},
			{ path: "a.md", version: 1 },
			"   ",
		);
		expect(message).toContain('Where: <img>  [selector: body>main:1>h1:1]');
		expect(message).toContain("- Sam: (comment deleted)\n- Former user: Still wrong\n- Orchestrator user: x");
		expect(message).not.toContain("Owner's note");
	});

	it("keeps the frame and the first and latest comments when the thread is too long", () => {
		const comments = Array.from({ length: 40 }, (_, index) => comment(String(index), `comment ${index} ${"word ".repeat(60)}`));
		const message = formatArtifactCommentMessage({ ...designThread, comments }, { path: "a.html", version: 2 }, "n".repeat(3000));
		expect(message.length).toBeLessThanOrEqual(MAX_ARTIFACT_COMMENT_MESSAGE_LENGTH);
		expect(message).toContain("- Sam: comment 0 ");
		expect(message).toContain("- Sam: comment 39 ");
		expect(message).toMatch(/- … \d+ more comments omitted …/);
		expect(message).toContain("[truncated]");
		expect(message.endsWith("They will republish it.")).toBe(true);
	});

	it("shortens the first comment, not the latest, when a large frame leaves little room", () => {
		const bigFrame = {
			...designThread,
			anchor: { path: `body${">p:1".repeat(99)}`, tag: "p", textHash: "9f2c00112233aabb", excerpt: "e".repeat(120) },
		};
		const artifact = { path: `docs/${"d".repeat(495)}`, version: 1 };
		const note = "n".repeat(1800);
		const message = formatArtifactCommentMessage(
			{ ...bigFrame, comments: [comment("1", "f".repeat(1200)), comment("2", "latest", aditi)] },
			artifact,
			note,
		);
		expect(message.length).toBeLessThanOrEqual(MAX_ARTIFACT_COMMENT_MESSAGE_LENGTH);
		expect(message).toContain("- Aditi: latest");
		expect(message).toMatch(/- Sam: f+ \[truncated\]/);
		expect(message).not.toContain("omitted");
		expect(message).toContain(`Owner's note: ${note}`);
		expect(message.endsWith("They will republish it.")).toBe(true);

		// When even the latest doesn't fit whole, it's shortened rather than dropped.
		const long = formatArtifactCommentMessage(
			{ ...bigFrame, comments: [comment("1", "f".repeat(1200)), comment("2", "m".repeat(1200)), comment("3", "z".repeat(1200), aditi)] },
			artifact,
			note,
		);
		expect(long.length).toBeLessThanOrEqual(MAX_ARTIFACT_COMMENT_MESSAGE_LENGTH);
		expect(long).toMatch(/- Aditi: z+ \[truncated\]/);
		expect(long).toContain("- … 1 more comment omitted …");
		expect(long.endsWith("They will republish it.")).toBe(true);
	});

	it("holds back room for the omission marker only when a comment is omitted", () => {
		const bigFrame = {
			...designThread,
			anchor: { path: `body${">p:1".repeat(99)}`, tag: "p", textHash: "9f2c00112233aabb", excerpt: "e".repeat(120) },
		};
		const artifact = { path: `docs/${"d".repeat(495)}`, version: 1 };
		const note = "n".repeat(1800);
		// The thread's room: the cap minus the frame, measured with a one-line thread.
		const probe = formatArtifactCommentMessage({ ...bigFrame, comments: [comment("1", "x")] }, artifact, note);
		const budget = MAX_ARTIFACT_COMMENT_MESSAGE_LENGTH - (probe.length - "- Sam: x".length);
		// A latest comment that fits beside the first's 200-character floor, but
		// not if the (unused) omission marker's room were held back too.
		const prefix = "- Aditi: ";
		const body = "w".repeat(budget - 201 - 10 - prefix.length - 1);
		const message = formatArtifactCommentMessage(
			{ ...bigFrame, comments: [comment("1", "f".repeat(1200)), comment("2", body, aditi)] },
			artifact,
			note,
		);
		expect(message.length).toBeLessThanOrEqual(MAX_ARTIFACT_COMMENT_MESSAGE_LENGTH);
		expect(message).toContain(`${prefix}${body}\n`);
		expect(message).not.toContain("omitted");
		expect(message).toMatch(/- Sam: f+ \[truncated\]/);
	});
});
