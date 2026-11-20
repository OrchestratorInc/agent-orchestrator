import { Bot } from "@/components/app-icons";
import { useEffect, useState } from "react";
import { cn } from "../../lib/utils";

// The onboarding illustration is a line drawing, so an agent's brand mark is
// drawn there as a flat outline: the shapes of its real logo (the same assets
// AgentAvatar shows), unfilled and traced in one thin stroke of the current
// colour. Each mark loads on demand, so the logos stay out of the main bundle.
// An agent without an SVG mark (or before its mark has loaded) gets a generic
// outline icon.

const SOURCES = import.meta.glob<string>("../../assets/agents/*.svg", { query: "?raw", import: "default" });

/** Logos shared between agents (AgentAvatar's LOGOS uses the same pairs). */
const SAME_MARK: Record<string, string> = { "opencode-v2": "opencode" };

/** What survives of a logo: its shapes and groups. Gradients, masks, styles,
 * embedded images and anything scriptable are dropped. */
const DROPPED_ELEMENTS = "defs, style, script, title, desc, image, mask, clipPath, linearGradient, radialGradient, pattern, filter, foreignObject, metadata";
const SHAPES = new Set(["path", "rect", "circle", "ellipse", "polygon", "polyline", "line"]);
/** Attributes that paint, clip or style; the outline sets its own. */
const DROPPED_ATTRIBUTES = /^(fill|fill-rule|fill-opacity|stroke.*|style|class|clip-path|clip-rule|mask|filter|opacity|href|xlink:href|on.*)$/i;

type Outline = { viewBox: string; markup: string };

const loaded = new Map<string, Outline | null>();
const loading = new Map<string, Promise<Outline | null>>();

function sourceFor(agent: string): (() => Promise<string>) | undefined {
	const base = SAME_MARK[agent] ?? agent;
	// A single mark reads on both backgrounds; otherwise the light-background one.
	return SOURCES[`../../assets/agents/${base}.svg`] ?? SOURCES[`../../assets/agents/${base}-light.svg`];
}

function toOutline(source: string): Outline | null {
	const svg = new DOMParser().parseFromString(source, "image/svg+xml").documentElement;
	if (svg.nodeName !== "svg") return null;
	const width = Number.parseFloat(svg.getAttribute("width") ?? "24");
	const height = Number.parseFloat(svg.getAttribute("height") ?? "24");
	const viewBox = svg.getAttribute("viewBox") ?? `0 0 ${width} ${height}`;
	for (const element of Array.from(svg.querySelectorAll(DROPPED_ELEMENTS))) element.remove();
	for (const element of Array.from(svg.querySelectorAll("*"))) {
		for (const attribute of Array.from(element.attributes)) {
			if (DROPPED_ATTRIBUTES.test(attribute.name)) element.removeAttribute(attribute.name);
		}
		// One screen pixel wide whatever the logo's own units.
		if (SHAPES.has(element.localName)) element.setAttribute("vector-effect", "non-scaling-stroke");
	}
	if (!Array.from(svg.querySelectorAll("*")).some((element) => SHAPES.has(element.localName))) return null;
	return { viewBox, markup: svg.innerHTML };
}

function loadOutline(agent: string): Promise<Outline | null> {
	const pending = loading.get(agent);
	if (pending) return pending;
	const source = sourceFor(agent);
	const next = (source ? source().then(toOutline) : Promise.resolve(null))
		.catch(() => null)
		.then((outline) => {
			loaded.set(agent, outline);
			return outline;
		});
	loading.set(agent, next);
	return next;
}

export function AgentOutline({ agent, className }: { agent?: string; className?: string }) {
	const [outline, setOutline] = useState<Outline | null | undefined>(() => (agent ? loaded.get(agent) : null));
	useEffect(() => {
		if (!agent) {
			setOutline(null);
			return;
		}
		let current = true;
		setOutline(loaded.get(agent));
		void loadOutline(agent).then((next) => {
			if (current) setOutline(next);
		});
		return () => {
			current = false;
		};
	}, [agent]);

	if (!outline) return <Bot aria-hidden="true" strokeWidth={1.25} className={cn("shrink-0", className)} />;
	return (
		<svg
			aria-hidden="true"
			viewBox={outline.viewBox}
			fill="none"
			stroke="currentColor"
			strokeWidth={1}
			strokeLinejoin="round"
			className={cn("shrink-0 overflow-visible", className)}
			// Markup from the app's own bundled logo assets, reduced to bare shapes above.
			dangerouslySetInnerHTML={{ __html: outline.markup }}
		/>
	);
}
