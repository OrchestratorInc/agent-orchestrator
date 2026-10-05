import { getAgentIdentity } from "@aoagents/product-ui";
import { cn } from "../../lib/utils";
import agyLogo from "../../assets/agents/agy.png";
import aiderLogo from "../../assets/agents/aider.png";
import ampLogo from "../../assets/agents/amp.svg";
import auggieLogo from "../../assets/agents/auggie.svg";
import autohandLogo from "../../assets/agents/autohand.svg";
import clineLogo from "../../assets/agents/cline.svg";
import claudeLogo from "../../assets/agents/claude.svg";
import claudeCodeLogo from "../../assets/agents/claude-code.svg";
import codexLogo from "../../assets/agents/codex.svg";
import continueLogo from "../../assets/agents/continue.png";
import copilotLogo from "../../assets/agents/copilot.svg";
import crushLogo from "../../assets/agents/crush.png";
import cursorLogo from "../../assets/agents/cursor.svg";
import devinLogo from "../../assets/agents/devin.png";
import deepseekHarnessLogo from "../../assets/agents/deepseek-harness.svg";
import droidLogo from "../../assets/agents/droid.png";
import fxLogo from "../../assets/agents/fx.svg";
import geminiLogo from "../../assets/agents/gemini.svg";
import gooseLogo from "../../assets/agents/goose.svg";
import grokLogo from "../../assets/agents/grok.png";
import kilocodeLogo from "../../assets/agents/kilocode.svg";
import kimiLogo from "../../assets/agents/kimi.png";
import kimchiLogo from "../../assets/agents/kimchi.svg";
import kiroLogo from "../../assets/agents/kiro.png";
import museLogo from "../../assets/agents/muse.png";
import mimoCodeLogo from "../../assets/agents/mimo-code.svg";
import ompLogo from "../../assets/agents/omp.png";
import opencodeLogo from "../../assets/agents/opencode.svg";
import piLogo from "../../assets/agents/pi.png";
import primeAgentLogo from "../../assets/agents/prime-agent.png";
import qwenLogo from "../../assets/agents/qwen.png";
import unrealAgentLogo from "../../assets/agents/unreal-agent.png";
import vibeLogo from "../../assets/agents/vibe.png";

const LOGOS: Readonly<Record<string, string>> = {
	claude: claudeLogo,
	"claude-code": claudeCodeLogo,
	codex: codexLogo,
	aider: aiderLogo,
	opencode: opencodeLogo,
	"opencode-v2": opencodeLogo,
	grok: grokLogo,
	droid: droidLogo,
	amp: ampLogo,
	agy: agyLogo,
	crush: crushLogo,
	cursor: cursorLogo,
	qwen: qwenLogo,
	gemini: geminiLogo,
	copilot: copilotLogo,
	goose: gooseLogo,
	auggie: auggieLogo,
	continue: continueLogo,
	devin: devinLogo,
	cline: clineLogo,
	kimi: kimiLogo,
	muse: museLogo,
	kiro: kiroLogo,
	kilocode: kilocodeLogo,
	vibe: vibeLogo,
	pi: piLogo,
	kimchi: kimchiLogo,
	"prime-agent": primeAgentLogo,
	autohand: autohandLogo,
	omp: ompLogo,
	fx: fxLogo,
	"unreal-agent": unrealAgentLogo,
	"mimo-code": mimoCodeLogo,
	"deepseek-harness": deepseekHarnessLogo,
};

// These three legacy raster marks include an opaque monochrome canvas. Blend
// only this explicit set so their canvas does not become a white or black tile
// in the opposite theme. Colored marks keep their source pixels unchanged.
const MONOCHROME_CANVAS_LOGOS = new Set(["droid", "grok", "unreal-agent"]);

function markClass(provider: string): string | undefined {
	// The bundled gray line marks need more contrast on the dark settings tile.
	if (["opencode", "opencode-v2", "copilot", "cline"].includes(provider)) {
		return "dark:brightness-0 dark:invert";
	}
	if (!MONOCHROME_CANVAS_LOGOS.has(provider)) return undefined;

	if (provider === "droid") {
		// Droid ships as a white mark on black. Invert only for light mode so
		// multiply removes its white canvas; screen removes the black canvas in dark mode.
		return "invert mix-blend-multiply dark:invert-0 dark:mix-blend-screen";
	}

	// Grok and Unreal Agent ship as black marks on white. Multiply removes
	// their white canvas in light mode; dark mode inverts that pair and screens
	// away the resulting black canvas.
	return "mix-blend-multiply dark:invert dark:mix-blend-screen";
}

export type HarnessLogoProps = {
	provider: string;
	/** Settings rows already render the provider name beside this mark. */
	decorative?: boolean;
};

/**
 * Settings-only provider mark. Every provider gets the same neutral 32px tile;
 * the existing brand asset is constrained to a 24px mark inside it.
 */
export function HarnessLogo({ provider, decorative = true }: HarnessLogoProps) {
	const logo = LOGOS[provider];
	const identity = getAgentIdentity(provider);
	const accessibility = decorative
		? { alt: "", "aria-hidden": true as const }
		: { alt: provider, title: provider };

	return (
		<span
			aria-hidden={decorative || undefined}
			className="inline-flex size-8 shrink-0 items-center justify-center overflow-hidden rounded-md border border-border bg-muted"
		>
			{logo ? (
				<img
					{...accessibility}
					className={cn("size-6 object-contain", markClass(provider))}
					draggable={false}
					src={logo}
				/>
			) : (
				<span
					aria-hidden={decorative || undefined}
					aria-label={decorative ? undefined : provider}
					className="text-sm font-semibold uppercase leading-none text-muted-foreground"
					role={decorative ? undefined : "img"}
					title={decorative ? undefined : provider}
				>
					{identity.initial}
				</span>
			)}
		</span>
	);
}
