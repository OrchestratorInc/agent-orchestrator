import {
	AgentAvatar as ProductAgentAvatar,
	type AgentAvatarProps,
	type AgentLogoSources,
} from "@aoagents/product-ui";
import agyLogo from "../assets/agents/agy.svg";
import aiderLogo from "../assets/agents/aider.svg";
import ampDarkLogo from "../assets/agents/amp-dark.svg";
import ampLightLogo from "../assets/agents/amp-light.svg";
import auggieDarkLogo from "../assets/agents/auggie-dark.svg";
import auggieLightLogo from "../assets/agents/auggie-light.svg";
import autohandDarkLogo from "../assets/agents/autohand-dark.svg";
import autohandLightLogo from "../assets/agents/autohand-light.svg";
import clineDarkLogo from "../assets/agents/cline-dark.svg";
import clineLightLogo from "../assets/agents/cline-light.svg";
import claudeLogo from "../assets/agents/claude.svg";
import claudeCodeLogo from "../assets/agents/claude-code.svg";
import codexLogo from "../assets/agents/codex.svg";
import codewhaleDarkLogo from "../assets/agents/codewhale-dark.svg";
import codewhaleLightLogo from "../assets/agents/codewhale-light.svg";
import commandCodeDarkLogo from "../assets/agents/command-code-dark.svg";
import commandCodeLightLogo from "../assets/agents/command-code-light.svg";
import continueDarkLogo from "../assets/agents/continue-dark.svg";
import continueLightLogo from "../assets/agents/continue-light.svg";
import copilotDarkLogo from "../assets/agents/copilot-dark.svg";
import copilotLightLogo from "../assets/agents/copilot-light.svg";
import crushLogo from "../assets/agents/crush.svg";
import cursorDarkLogo from "../assets/agents/cursor-dark.svg";
import cursorLightLogo from "../assets/agents/cursor-light.svg";
import devinDarkLogo from "../assets/agents/devin-dark.svg";
import devinLightLogo from "../assets/agents/devin-light.svg";
import deepseekHarnessDarkLogo from "../assets/agents/deepseek-harness-dark.svg";
import deepseekHarnessLightLogo from "../assets/agents/deepseek-harness-light.svg";
import droidDarkLogo from "../assets/agents/droid-dark.svg";
import droidLightLogo from "../assets/agents/droid-light.svg";
import fxDarkLogo from "../assets/agents/fx-dark.svg";
import fxLightLogo from "../assets/agents/fx-light.svg";
import geminiLogo from "../assets/agents/gemini.svg";
import gooseDarkLogo from "../assets/agents/goose-dark.svg";
import gooseLightLogo from "../assets/agents/goose-light.svg";
import grokDarkLogo from "../assets/agents/grok-dark.svg";
import grokLightLogo from "../assets/agents/grok-light.svg";
import kilocodeDarkLogo from "../assets/agents/kilocode-dark.svg";
import kilocodeLightLogo from "../assets/agents/kilocode-light.svg";
import kimiDarkLogo from "../assets/agents/kimi-dark.svg";
import kimiLightLogo from "../assets/agents/kimi-light.svg";
import kimchiLogo from "../assets/agents/kimchi.svg";
import kiroLogo from "../assets/agents/kiro.svg";
import museLogo from "../assets/agents/muse.svg";
import mimoCodeLogo from "../assets/agents/mimo-code.svg";
import ompLogo from "../assets/agents/omp.svg";
import openhandsDarkLogo from "../assets/agents/openhands-dark.svg";
import openhandsLightLogo from "../assets/agents/openhands-light.svg";
import opencodeDarkLogo from "../assets/agents/opencode-dark.svg";
import opencodeLightLogo from "../assets/agents/opencode-light.svg";
import piDarkLogo from "../assets/agents/pi-dark.svg";
import piLightLogo from "../assets/agents/pi-light.svg";
import primeAgentDarkLogo from "../assets/agents/prime-agent-dark.svg";
import primeAgentLightLogo from "../assets/agents/prime-agent-light.svg";
import qwenDarkLogo from "../assets/agents/qwen-dark.svg";
import qwenLightLogo from "../assets/agents/qwen-light.svg";
import unrealAgentDarkLogo from "../assets/agents/unreal-agent-dark.png";
import unrealAgentLightLogo from "../assets/agents/unreal-agent-light.png";
import vibeLogo from "../assets/agents/vibe.svg";

// Official brand marks keyed by the harness name AO stores on session.provider.
// A { light, dark } pair is used where the brand ships separate marks for each
// background; a single mark reads on both. Agents without an asset fall back to
// a lettered tile.
const LOGOS: AgentLogoSources = {
	codex: codexLogo,
	codewhale: { light: codewhaleLightLogo, dark: codewhaleDarkLogo },
	"command-code": { light: commandCodeLightLogo, dark: commandCodeDarkLogo },
	"claude-code": claudeCodeLogo,
	claude: claudeLogo,
	cursor: { light: cursorLightLogo, dark: cursorDarkLogo },
	opencode: { light: opencodeLightLogo, dark: opencodeDarkLogo },
	"opencode-v2": { light: opencodeLightLogo, dark: opencodeDarkLogo },
	copilot: { light: copilotLightLogo, dark: copilotDarkLogo },
	aider: aiderLogo,
	grok: { light: grokLightLogo, dark: grokDarkLogo },
	gemini: geminiLogo,
	droid: { light: droidLightLogo, dark: droidDarkLogo },
	fx: { light: fxLightLogo, dark: fxDarkLogo },
	crush: crushLogo,
	qwen: { light: qwenLightLogo, dark: qwenDarkLogo },
	goose: { light: gooseLightLogo, dark: gooseDarkLogo },
	continue: { light: continueLightLogo, dark: continueDarkLogo },
	devin: { light: devinLightLogo, dark: devinDarkLogo },
	kimi: { light: kimiLightLogo, dark: kimiDarkLogo },
	muse: museLogo,
	omp: ompLogo,
	openhands: { light: openhandsLightLogo, dark: openhandsDarkLogo },
	kiro: kiroLogo,
	kilocode: { light: kilocodeLightLogo, dark: kilocodeDarkLogo },
	vibe: vibeLogo,
	pi: { light: piLightLogo, dark: piDarkLogo },
	kimchi: kimchiLogo,
	"prime-agent": { light: primeAgentLightLogo, dark: primeAgentDarkLogo },
	amp: { light: ampLightLogo, dark: ampDarkLogo },
	cline: { light: clineLightLogo, dark: clineDarkLogo },
	agy: agyLogo,
	auggie: { light: auggieLightLogo, dark: auggieDarkLogo },
	autohand: { light: autohandLightLogo, dark: autohandDarkLogo },
	"unreal-agent": { light: unrealAgentLightLogo, dark: unrealAgentDarkLogo },
	"mimo-code": mimoCodeLogo,
	"deepseek-harness": { light: deepseekHarnessLightLogo, dark: deepseekHarnessDarkLogo },
};

/**
 * Agent mark for board/task cards: the harness's real brand logo rendered bare —
 * no tile, border, or background — so each brand's own shape shows (codex,
 * claude, cursor, … carry their own rounded backgrounds). Agents without an
 * asset fall back to a bare initial. Kept small so the title stays the hero.
 *
 * The provider is exposed as the accessible name (alt / aria-label), not just a
 * hover title, so surfaces that show the logo in place of visible agent text —
 * e.g. the archive cards — still name the agent for screen readers.
 */
export function AgentAvatar({ provider, className, decorative = false }: AgentAvatarProps) {
	return <ProductAgentAvatar className={className} decorative={decorative} logoSources={LOGOS} provider={provider} />;
}

export type { AgentAvatarProps };
