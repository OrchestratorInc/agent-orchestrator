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
	/** Capture the element too. The daemon asks only once it will prompt. */
	image?: boolean;
	snapshot: () => Promise<{ refs: unknown }>;
	page: PanelApprovalPage;
};

export type PanelApprovalDescription = {
	target: Partial<ApprovalElementFacts> & { pageUrl: string; pageTitle: string; instruction?: string };
	/** The ref act matched, so an approved act can be bound to it. */
	resolvedRef?: string;
	/**
	 * The element was named but its form could not be read; the daemon asks
	 * rather than trust a benign verdict.
	 */
	partial?: true;
	/** For Enter: the id marked on the focused element, checked again at dispatch. */
	focus?: string;
	image?: string;
	untrustedExternalContent: true;
};

export async function describePanelApprovalTarget(input: PanelApprovalInput): Promise<PanelApprovalDescription> {
	const page = { pageUrl: sanitizeBrowserURL(input.page.url), pageTitle: sanitizeBrowserTitle(input.page.title) };
	let role = "";
	let name = "";
	let resolvedRef: string | undefined;
	let request: ApprovalDescribeRequest = { mode: "focused" };
	if (input.action !== "press") {
		const { refs } = await input.snapshot();
		const entries = parseSnapshotEntries(refs);
		if (input.action === "act") {
			const match = matchInstruction(input.instruction ?? "", refs, { nth: input.nth });
			if (match.outcome === "matched") ({ role, name, ref: resolvedRef } = match.candidate);
		} else if (input.ref) {
			const wanted = input.ref.replace(/^@/, "");
			const hit = entries.find((entry) => entry.ref === wanted);
			if (hit) ({ role, name } = hit);
		}
		if (!role && !name) {
			return { target: { ...page, instruction: input.instruction }, untrustedExternalContent: true };
		}
		// The DOM read finds the first visible control with this role and name;
		// when several share them it could describe another control's form.
		// Report only what the snapshot knows about the chosen one.
		if (entries.filter((entry) => entry.role === role && entry.name === name).length > 1) {
			return {
				target: { role, name, ...page, ...(input.instruction ? { instruction: input.instruction } : {}) },
				...(resolvedRef ? { resolvedRef } : {}),
				partial: true,
				untrustedExternalContent: true,
			};
		}
		request = { mode: "match", role, name };
	}

	let facts: ApprovalElementFacts | null = null;
	let viewport = { width: 0, height: 0 };
	let focus: string | undefined;
	try {
		const evaluated = (await input.page.evaluate(approvalDescribeScript(request))) as
			| { facts?: ApprovalElementFacts | null; viewport?: { width: number; height: number }; focus?: string }
			| undefined;
		facts = evaluated?.facts ?? null;
		viewport = evaluated?.viewport ?? viewport;
		focus = evaluated?.focus;
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
	const crop = input.image ? approvalCropRect(facts?.rect, viewport) : undefined;
	let image: string | undefined;
	if (crop) {
		try {
			image = await input.page.capture(crop);
		} catch {
			image = undefined;
		}
	}
	return {
		target,
		...(resolvedRef ? { resolvedRef } : {}),
		// A named element whose DOM could not be read has no form facts.
		...(!facts && (role || name) ? { partial: true as const } : {}),
		...(focus ? { focus } : {}),
		...(image ? { image } : {}),
		untrustedExternalContent: true,
	};
}
