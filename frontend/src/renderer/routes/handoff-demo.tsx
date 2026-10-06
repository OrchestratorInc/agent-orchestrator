import { createFileRoute } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { AgentSwitchHandoffVisual, agentSwitchHandoffSteps } from "../components/AgentSwitchHandoffVisual";
import type { AgentSwitchPresentation } from "../lib/agent-switch-presentation";
import { AGENT_LABELS, AGENT_OPTIONS } from "../lib/agent-options";
import { cn } from "../lib/utils";

export const Route = createFileRoute("/handoff-demo")({
	component: HandoffDemoRoute,
});

type Stage = (typeof agentSwitchHandoffSteps)[number]["key"];

const AUTO_ADVANCE_MS = 1400;

function HandoffDemoRoute() {
	const { t } = useTranslation();
	const [stage, setStage] = useState<Stage>("preparing");
	const [fromHarness, setFromHarness] = useState<string>("codex");
	const [targetHarness, setTargetHarness] = useState<string>("claude-code");
	const [variant, setVariant] = useState<"full" | "compact">("full");
	const [playing, setPlaying] = useState(false);

	useEffect(() => {
		if (!playing) return;
		const id = window.setInterval(() => {
			setStage((current) => {
				const index = agentSwitchHandoffSteps.findIndex((step) => step.key === current);
				return agentSwitchHandoffSteps[(index + 1) % agentSwitchHandoffSteps.length].key;
			});
		}, AUTO_ADVANCE_MS);
		return () => window.clearInterval(id);
	}, [playing]);

	const currentStepIndex = agentSwitchHandoffSteps.findIndex((step) => step.key === stage);

	return (
		<div className="flex min-h-screen flex-col items-center gap-8 bg-background px-6 py-12 text-foreground">
			<header className="flex flex-col items-center gap-2 text-center">
				<h1 className="font-mono text-control font-medium">Agent switch — handoff animation demo</h1>
				<p className="max-w-md text-caption leading-4 text-muted-foreground">
					Dotted curve, animated orb, and green-tick steps. Drive the stage sequence manually or auto-play it.
				</p>
			</header>

			<section className="w-full max-w-xl rounded-xl border border-border-strong bg-surface/95 px-8 py-10 shadow-xl shadow-black/20">
				<AgentSwitchHandoffVisual
					fromHarness={fromHarness}
					targetHarness={targetHarness}
					stage={stage as AgentSwitchPresentation["stage"]}
					variant={variant}
				/>
			</section>

			<section className="flex w-full max-w-xl flex-col gap-4">
				<div className="flex flex-wrap items-end justify-center gap-3">
					<label className="flex flex-col gap-1 text-caption text-muted-foreground">
						From
						<select
							value={fromHarness}
							onChange={(event) => setFromHarness(event.target.value)}
							className="rounded-md border border-border-strong bg-surface px-2 py-1.5 text-caption text-foreground"
						>
							{AGENT_OPTIONS.map((id) => (
								<option key={id} value={id}>
									{AGENT_LABELS[id]}
								</option>
							))}
						</select>
					</label>

					<label className="flex flex-col gap-1 text-caption text-muted-foreground">
						To
						<select
							value={targetHarness}
							onChange={(event) => setTargetHarness(event.target.value)}
							className="rounded-md border border-border-strong bg-surface px-2 py-1.5 text-caption text-foreground"
						>
							{AGENT_OPTIONS.map((id) => (
								<option key={id} value={id}>
									{AGENT_LABELS[id]}
								</option>
							))}
						</select>
					</label>

					<label className="flex flex-col gap-1 text-caption text-muted-foreground">
						Variant
						<select
							value={variant}
							onChange={(event) => setVariant(event.target.value as "full" | "compact")}
							className="rounded-md border border-border-strong bg-surface px-2 py-1.5 text-caption text-foreground"
						>
							<option value="full">Full</option>
							<option value="compact">Compact</option>
						</select>
					</label>
				</div>

				<div className="flex flex-wrap items-center justify-center gap-2">
					{agentSwitchHandoffSteps.map((step) => (
						<button
							key={step.key}
							type="button"
							onClick={() => {
								setPlaying(false);
								setStage(step.key);
							}}
							className={cn(
								"rounded-md border px-3 py-1.5 text-caption transition-colors",
								stage === step.key
									? "border-accent bg-accent/20 text-foreground"
									: "border-border-strong bg-surface text-muted-foreground hover:text-foreground",
							)}
						>
							{t(step.labelKey)}
						</button>
					))}

					<button
						type="button"
						onClick={() => setPlaying((value) => !value)}
						className={cn(
							"rounded-md border px-3 py-1.5 text-caption font-medium transition-colors",
							playing
								? "border-accent bg-accent text-accent-foreground"
								: "border-border-strong bg-surface text-foreground",
						)}
					>
						{playing ? "Pause" : "Auto-play"}
					</button>
				</div>

				<p className="text-center font-mono text-caption text-muted-foreground">
					Step {currentStepIndex + 1}/{agentSwitchHandoffSteps.length} — {t(agentSwitchHandoffSteps[currentStepIndex].labelKey)}
				</p>
			</section>
		</div>
	);
}
