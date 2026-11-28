import type { TFunction } from "i18next";
import {
	artifactCommentAnchorKind,
	artifactCommentRegionPlace,
	formatArtifactCommentTime,
	isPointRegion,
	type ArtifactCommentAnchor,
	type ArtifactCommentRegionPlace,
} from "../../../shared/artifact-comments";

function place(t: TFunction, value: ArtifactCommentRegionPlace): string {
	switch (value) {
		case "top-left": return t("artifactComments.placeTopLeft");
		case "top": return t("artifactComments.placeTop");
		case "top-right": return t("artifactComments.placeTopRight");
		case "left": return t("artifactComments.placeLeft");
		case "center": return t("artifactComments.placeCenter");
		case "right": return t("artifactComments.placeRight");
		case "bottom-left": return t("artifactComments.placeBottomLeft");
		case "bottom": return t("artifactComments.placeBottom");
		case "bottom-right": return t("artifactComments.placeBottomRight");
	}
}

/**
 * Where a thread is, in words, for lines, images and videos: "Lines 12–14",
 * "Point near top-left", "At 01:23, region near center". Null for a page
 * element, whose place the page itself shows.
 */
export function artifactCommentAnchorText(t: TFunction, anchor: ArtifactCommentAnchor | null | undefined): string | null {
	if (!anchor) return null;
	switch (artifactCommentAnchorKind(anchor)) {
		case "lines": {
			if (!anchor.lines) return null;
			const { start, end } = anchor.lines;
			const rows = anchor.tag === "tr";
			if (start === end) return rows ? t("artifactComments.anchorRow", { n: start }) : t("artifactComments.anchorLine", { n: start });
			return rows ? t("artifactComments.anchorRows", { start, end }) : t("artifactComments.anchorLines", { start, end });
		}
		case "image": {
			const region = anchor.region;
			if (!region) return null;
			const where = place(t, artifactCommentRegionPlace(region));
			return isPointRegion(region) ? t("artifactComments.anchorPoint", { place: where }) : t("artifactComments.anchorRegion", { place: where });
		}
		case "video": {
			const time = formatArtifactCommentTime(anchor.time ?? 0);
			const region = anchor.region;
			if (!region) return t("artifactComments.anchorAt", { time });
			const where = place(t, artifactCommentRegionPlace(region));
			return isPointRegion(region)
				? t("artifactComments.anchorAtPoint", { time, place: where })
				: t("artifactComments.anchorAtRegion", { time, place: where });
		}
		default:
			return null;
	}
}
