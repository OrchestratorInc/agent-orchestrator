import { describe, expect, it } from "vitest";
import type * as Contract from "../../../packages/cloud-client/src/types";
import {
	ARTIFACT_COMMENT_EMPTY_TEXT_HASH,
	artifactAnchorTextHash,
	artifactCommentAnchorKey,
	artifactCommentAnchorLabel,
	artifactCommentContainedBox,
	artifactCommentMarkers,
	artifactCommentPointIn,
	artifactCommentRegionBox,
	artifactCommentRegionFromDrag,
	artifactCommentRegionPlace,
	artifactCommentWhere,
	formatArtifactCommentMessage,
	formatArtifactCommentTime,
	groupArtifactCommentThreads,
	imageCommentAnchor,
	isArtifactCommentAnchor,
	isKnownArtifactCommentAnchor,
	parseBridgeToViewerMessage,
	parseViewerToBridgeMessage,
	videoCommentAnchor,
	type ArtifactCommentAnchor,
	type ArtifactCommentLineRange,
	type ArtifactCommentRegion,
	type ArtifactCommentThread,
} from "./artifact-comments";

type Equal<A, B> = (<T>() => T extends A ? 1 : 2) extends <T>() => T extends B ? 1 : 2 ? true : false;
const contractPins: [Equal<ArtifactCommentLineRange, Contract.ArtifactShareCommentLineRange>, Equal<ArtifactCommentRegion, Contract.ArtifactShareCommentRegion>] = [
	true,
	true,
];

const line = (n: number, end = n, tag = "span"): ArtifactCommentAnchor => ({
	path: tag === "tr" ? `body>main:1>table:1>tbody:1>tr:${n}` : `body>main:1>pre:1>code:1>span:${n}`,
	tag,
	textHash: "9f2c00112233aabb",
	excerpt: "const retry = 3;",
	kind: "lines",
	lines: { start: n, end },
});

function thread(id: string, anchor: ArtifactCommentAnchor, createdAt: string, status: "open" | "resolved" = "open"): ArtifactCommentThread {
	return {
		id,
		version: 1,
		status,
		anchor,
		createdAt,
		updatedAt: createdAt,
		resolvedBy: null,
		resolvedAt: null,
		sentToAgentAt: null,
		sentToAgentCount: 0,
		comments: [
			{ id: `${id}-1`, threadId: id, author: { id: "u", name: "Sam" }, clientId: `c-${id}`, body: "Look here", createdAt, editedAt: null, deleted: false },
		],
	};
}

describe("anchor kinds on the wire", () => {
	it("pins the new shapes to the Cloud contract", () => {
		expect(contractPins.every(Boolean)).toBe(true);
	});

	it("leaves an element anchor exactly as it was", () => {
		const element = { path: "body>main:1>h1:1", tag: "h1", textHash: "9f2c00112233aabb", excerpt: "Title" };
		expect(isArtifactCommentAnchor(element)).toBe(true);
		expect(isArtifactCommentAnchor({ ...element, kind: "element" })).toBe(true);
		expect(artifactCommentAnchorKey(element)).toBe("body>main:1>h1:1");
		expect(artifactCommentAnchorLabel(element)).toBeNull();
		// An element carries none of the new fields.
		expect(isArtifactCommentAnchor({ ...element, lines: { start: 1, end: 1 } })).toBe(false);
		expect(isArtifactCommentAnchor({ ...element, time: 3 })).toBe(false);
	});

	it("validates lines, image and video anchors", () => {
		expect(isArtifactCommentAnchor(line(12, 14))).toBe(true);
		expect(isArtifactCommentAnchor({ ...line(12), lines: { start: 3, end: 2 } })).toBe(false);
		expect(isArtifactCommentAnchor({ ...line(12), lines: { start: 0, end: 2 } })).toBe(false);
		expect(isArtifactCommentAnchor({ ...line(12), lines: { start: 1.5, end: 2 } })).toBe(false);
		expect(isArtifactCommentAnchor({ ...line(12), lines: undefined })).toBe(false);

		const image = imageCommentAnchor({ x: 0.1, y: 0.2, w: 0.3, h: 0.4 });
		expect(isArtifactCommentAnchor(image)).toBe(true);
		expect(isArtifactCommentAnchor({ ...image, region: { x: 0.8, y: 0, w: 0.3, h: 0 } })).toBe(false);
		expect(isArtifactCommentAnchor({ ...image, region: undefined })).toBe(false);
		expect(isArtifactCommentAnchor({ ...image, time: 1 })).toBe(false);

		const video = videoCommentAnchor(83.25);
		expect(isArtifactCommentAnchor(video)).toBe(true);
		expect(isArtifactCommentAnchor(videoCommentAnchor(83.25, { x: 0.5, y: 0.5, w: 0, h: 0 }))).toBe(true);
		expect(isArtifactCommentAnchor({ ...video, time: -1 })).toBe(false);
		expect(isArtifactCommentAnchor({ ...video, time: undefined })).toBe(false);
		expect(isArtifactCommentAnchor({ ...video, time: Number.NaN })).toBe(false);
	});

	it("treats an unknown kind as one to skip", () => {
		const future = { ...line(3), kind: "pdf-page", lines: undefined } as unknown as ArtifactCommentAnchor;
		expect(isArtifactCommentAnchor(future)).toBe(false);
		expect(isKnownArtifactCommentAnchor(future)).toBe(false);
	});

	it("gives media anchors the empty-text hash and fixed paths", async () => {
		expect(await artifactAnchorTextHash("")).toBe(ARTIFACT_COMMENT_EMPTY_TEXT_HASH);
		expect(imageCommentAnchor({ x: 0, y: 0, w: 0, h: 0 })).toMatchObject({ path: "body>img:1", tag: "img", excerpt: "", kind: "image" });
		expect(videoCommentAnchor(1.23456)).toMatchObject({ path: "body>video:1", tag: "video", kind: "video", time: 1.235 });
		expect(videoCommentAnchor(-5).time).toBe(0);
		expect(videoCommentAnchor(5)).not.toHaveProperty("region");
	});
});

describe("picking on a picture", () => {
	it("turns a short drag into a point and a longer one into a rectangle", () => {
		expect(artifactCommentRegionFromDrag({ x: 0.5, y: 0.5 }, { x: 0.505, y: 0.502 })).toEqual({ x: 0.5, y: 0.5, w: 0, h: 0 });
		expect(artifactCommentRegionFromDrag({ x: 0.6, y: 0.7 }, { x: 0.2, y: 0.1 })).toEqual({ x: 0.2, y: 0.1, w: 0.4, h: 0.6 });
		// Clamped to the picture.
		expect(artifactCommentRegionFromDrag({ x: -0.2, y: 0.5 }, { x: 1.4, y: 0.9 })).toEqual({ x: 0, y: 0.5, w: 1, h: 0.4 });
		const region = artifactCommentRegionFromDrag({ x: 0.33333333, y: 0.1 }, { x: 0.99999, y: 0.3 });
		expect(region.x + region.w).toBeLessThanOrEqual(1);
	});

	it("maps the pointer through object-fit: contain letterboxing", () => {
		// A 200×100 picture in a 400×400 box: 400×200, 100 px bars above and below.
		const box = artifactCommentContainedBox({ width: 200, height: 100 }, { width: 400, height: 400 });
		expect(box).toEqual({ left: 0, top: 100, width: 400, height: 200 });
		expect(artifactCommentPointIn(box, 100, 150)).toEqual({ x: 0.25, y: 0.25 });
		expect(artifactCommentPointIn(box, 100, 20)).toEqual({ x: 0.25, y: 0 });
		expect(artifactCommentRegionBox(box, { x: 0.5, y: 0.5, w: 0.25, h: 0.5 })).toEqual({ left: 200, top: 200, width: 100, height: 100 });
	});

	it("names the ninth of the picture a region is in", () => {
		expect(artifactCommentRegionPlace({ x: 0.05, y: 0.05, w: 0, h: 0 })).toBe("top-left");
		expect(artifactCommentRegionPlace({ x: 0.4, y: 0.4, w: 0.2, h: 0.2 })).toBe("center");
		expect(artifactCommentRegionPlace({ x: 0.9, y: 0.5, w: 0, h: 0 })).toBe("right");
		expect(artifactCommentRegionPlace({ x: 0.5, y: 0.9, w: 0, h: 0 })).toBe("bottom");
	});
});

describe("labels in plain words", () => {
	it("formats times", () => {
		expect(formatArtifactCommentTime(0)).toBe("00:00");
		expect(formatArtifactCommentTime(83.9)).toBe("01:23");
		expect(formatArtifactCommentTime(3723)).toBe("1:02:03");
	});

	it("labels each kind", () => {
		expect(artifactCommentAnchorLabel(line(12))).toBe("Line 12");
		expect(artifactCommentAnchorLabel(line(12, 14))).toBe("Lines 12–14");
		expect(artifactCommentAnchorLabel(line(3, 5, "tr"))).toBe("Rows 3–5");
		expect(artifactCommentAnchorLabel(imageCommentAnchor({ x: 0.1, y: 0.1, w: 0, h: 0 }))).toBe("Point near top-left");
		expect(artifactCommentAnchorLabel(imageCommentAnchor({ x: 0.3, y: 0.3, w: 0.4, h: 0.4 }))).toBe("Region near center");
		expect(artifactCommentAnchorLabel(videoCommentAnchor(83))).toBe("At 01:23");
		expect(artifactCommentAnchorLabel(videoCommentAnchor(83, { x: 0.7, y: 0.8, w: 0.2, h: 0.1 }))).toBe("At 01:23, region near bottom-right");
	});

	it("tells the agent where in words", () => {
		expect(artifactCommentWhere(line(12, 14))).toBe('Where: lines 12-14 "const retry = 3;"');
		expect(artifactCommentWhere(line(12))).toBe('Where: line 12 "const retry = 3;"');
		expect(artifactCommentWhere(line(4, 4, "tr"))).toBe('Where: row 4 "const retry = 3;"');
		expect(artifactCommentWhere(videoCommentAnchor(83))).toBe("Where: at 01:23 in the video");
		expect(artifactCommentWhere(videoCommentAnchor(83, { x: 0.1, y: 0.1, w: 0.2, h: 0.1 }))).toBe(
			"Where: at 01:23 in the video, region near top-left of the frame (x 10%–30%, y 10%–20%)",
		);
		expect(artifactCommentWhere(imageCommentAnchor({ x: 0.12, y: 0.3, w: 0, h: 0 }))).toBe(
			"Where: point near top-left of the image (x 12%, y 30%)",
		);
		const message = formatArtifactCommentMessage(thread("t1", videoCommentAnchor(83), "2026-11-25T10:00:00Z"), { path: "demo.mp4", version: 2 });
		expect(message).toContain("File: demo.mp4  (shared link version 2)\nWhere: at 01:23 in the video\n");
	});
});

describe("grouping the new kinds", () => {
	it("groups a range with the single line it starts on, and orders by the bridge", () => {
		const threads = [thread("a", line(12), "2026-11-25T10:00:00Z"), thread("b", line(12, 14), "2026-11-25T11:00:00Z"), thread("c", line(3), "2026-11-25T12:00:00Z")];
		const groups = groupArtifactCommentThreads(threads, [
			{ path: line(12).path, top: 400, found: true },
			{ path: line(3).path, top: 60, found: true },
		]);
		expect(groups.open.map((group) => group.path)).toEqual([line(3).path, line(12).path]);
		expect(groups.open[1].threads.map((t) => t.id)).toEqual(["b", "a"]);
		expect(artifactCommentMarkers(threads)).toEqual([
			{ path: line(12).path, textHash: "9f2c00112233aabb", count: 2, lines: { start: 12, end: 14 } },
			{ path: line(3).path, textHash: "9f2c00112233aabb", count: 1, lines: { start: 3, end: 3 } },
		]);
	});

	it("orders image groups top to bottom and video groups by time, with no bridge", () => {
		const image = groupArtifactCommentThreads([
			thread("low", imageCommentAnchor({ x: 0.1, y: 0.8, w: 0, h: 0 }), "2026-11-25T10:00:00Z"),
			thread("high", imageCommentAnchor({ x: 0.9, y: 0.1, w: 0, h: 0 }), "2026-11-25T11:00:00Z"),
		]);
		expect(image.open.map((group) => group.threads[0].id)).toEqual(["high", "low"]);
		const video = groupArtifactCommentThreads([
			thread("late", videoCommentAnchor(90), "2026-11-25T10:00:00Z"),
			thread("early", videoCommentAnchor(5), "2026-11-25T11:00:00Z"),
			thread("early-again", videoCommentAnchor(5), "2026-11-25T12:00:00Z"),
		]);
		expect(video.open.map((group) => group.threads.map((t) => t.id))).toEqual([["early-again", "early"], ["late"]]);
		expect(video.open[0].path).toBe("video:5000");
		// Media never goes to the bridge.
		expect(artifactCommentMarkers([thread("v", videoCommentAnchor(5), "2026-11-25T10:00:00Z")])).toEqual([]);
	});

	it("leaves threads of an unknown kind out of groups, markers and counts", () => {
		const future = { ...line(3), kind: "pdf-page" } as unknown as ArtifactCommentAnchor;
		const groups = groupArtifactCommentThreads([thread("x", future, "2026-11-25T10:00:00Z"), thread("y", line(4), "2026-11-25T10:00:00Z")]);
		expect(groups.openCount).toBe(1);
		expect(groups.open.flatMap((group) => group.threads.map((t) => t.id))).toEqual(["y"]);
		expect(artifactCommentMarkers([thread("x", future, "2026-11-25T10:00:00Z")])).toEqual([]);
	});
});

describe("bridge protocol additions", () => {
	it("passes a lines pick through, and drops a malformed one", () => {
		const anchor = { path: line(12).path, tag: "span", textHash: "9f2c00112233aabb", excerpt: "x", kind: "lines", lines: { start: 12, end: 14 } };
		expect(parseBridgeToViewerMessage({ type: "pick", anchor })).toEqual({ type: "pick", anchor });
		expect(parseBridgeToViewerMessage({ type: "pick", anchor: { ...anchor, lines: { start: 14, end: 12 } } })).toBeNull();
		expect(parseBridgeToViewerMessage({ type: "pick", anchor: { ...anchor, kind: "image" } })).toBeNull();
		expect(parseBridgeToViewerMessage({ type: "pick", anchor: { ...anchor, kind: undefined } })).toBeNull();
	});

	it("accepts text mode and a marker's line span", () => {
		expect(parseViewerToBridgeMessage({ type: "mode", on: true, kind: "text" })).toEqual({ type: "mode", on: true, kind: "text" });
		const marker = { path: line(12).path, textHash: "9f2c00112233aabb", count: 1, lines: { start: 12, end: 14 } };
		expect(parseViewerToBridgeMessage({ type: "anchors", anchors: [marker] })).toEqual({ type: "anchors", anchors: [marker] });
		expect(parseViewerToBridgeMessage({ type: "anchors", anchors: [{ ...marker, lines: { start: 0, end: 1 } }] })).toBeNull();
	});
});
