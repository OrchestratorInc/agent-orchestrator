// The panel runtime's answer to the daemon's internal "describe" action: what
// a click, act, or Enter would act on, so the approval gate can decide whether
// to ask the user first. Refs live inside agent-browser, so the element's role
// and name come from its snapshot, and the DOM facts (form, fields, position)
// from the visible control with that role and name.

import {
	approvalCropRect,
	approvalDescribeScript,
	type ApprovalDescribeRequest,
	type ApprovalElementFacts,
} from "../shared/browser-approval-describe";
import { sanitizeBrowserTitle, sanitizeBrowserURL } from "../shared/browser-sanitize";
import { matchInstruction, parseSnapshotEntries } from "./browser-act-matcher";

export type PanelApprovalPage = {
	url: string;
	title: string;
	/** Evaluate source text in the page (an isolated world). */
	evaluate: (code: string) => Promise<unknown>;
	/** Capture a viewport rectangle as base64 PNG, or undefined if unavailable. */
	capture: (rect: { x: number; y: number; width: number; height: number }) => Promise<string | undefined>;
};

export type PanelApprovalInput = {
	action: string;
	ref?: string;
	instruction?: string;
	nth?: number;
	snapshot: () => Promise<{ refs: unknown }>;
	page: PanelApprovalPage;
};

export type PanelApprovalDescription = {
	target: Partial<ApprovalElementFacts> & { pageUrl: string; pageTitle: string; instruction?: string };
	image?: string;
	untrustedExternalContent: true;
};

export async function describePanelApprovalTarget(input: PanelApprovalInput): Promise<PanelApprovalDescription> {
	const page = { pageUrl: sanitizeBrowserURL(input.page.url), pageTitle: sanitizeBrowserTitle(input.page.title) };
	let role = "";
	let name = "";
	let request: ApprovalDescribeRequest = { mode: "focused" };
	if (input.action !== "press") {
		const { refs } = await input.snapshot();
		if (input.action === "act") {
			const match = matchInstruction(input.instruction ?? "", refs, { nth: input.nth });
			if (match.outcome === "matched") ({ role, name } = match.candidate);
		} else if (input.ref) {
			const wanted = input.ref.replace(/^@/, "");
			const hit = parseSnapshotEntries(refs).find((entry) => entry.ref === wanted);
			if (hit) ({ role, name } = hit);
		}
		if (!role && !name) {
			return { target: { ...page, instruction: input.instruction }, untrustedExternalContent: true };
		}
		request = { mode: "match", role, name };
	}

	let facts: ApprovalElementFacts | null = null;
	let viewport = { width: 0, height: 0 };
	try {
		const evaluated = (await input.page.evaluate(approvalDescribeScript(request))) as
			| { facts?: ApprovalElementFacts | null; viewport?: { width: number; height: number } }
			| undefined;
		facts = evaluated?.facts ?? null;
		viewport = evaluated?.viewport ?? viewport;
	} catch {
		// A page that refuses scripts still has its accessibility role and name.
	}
	const target = {
		...(facts ?? {}),
		// The accessibility tree's role and name are what the agent addressed.
		...(role ? { role } : {}),
		...(name ? { name } : {}),
		...page,
		...(input.instruction ? { instruction: input.instruction } : {}),
	};
	const crop = approvalCropRect(facts?.rect, viewport);
	let image: string | undefined;
	if (crop) {
		try {
			image = await input.page.capture(crop);
		} catch {
			image = undefined;
		}
	}
	return { target, ...(image ? { image } : {}), untrustedExternalContent: true };
}
