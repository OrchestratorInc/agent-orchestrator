import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useId } from "react";
import { useTranslation } from "react-i18next";
import {
	ACQUISITION_CHANNELS,
	type AcquisitionAnswer,
	type AcquisitionChannel,
	type AcquisitionChannelState,
} from "../../../shared/acquisition-channel";
import { aoBridge } from "../../lib/bridge";
import { reportOnboardingStep, useOnboardingStep } from "../../lib/onboarding-telemetry";
import { captureRendererEvent } from "../../lib/telemetry";
import { cn } from "../../lib/utils";
import type { MessageKey } from "../../i18n/messages";
import { Button } from "../ui/button";

export const acquisitionChannelQueryKey = ["app-state", "acquisition-channel"] as const;

const CHANNEL_LABEL_KEYS: Record<AcquisitionChannel, MessageKey> = {
	x_twitter: "onboarding.attribution.channel.x_twitter",
	youtube: "onboarding.attribution.channel.youtube",
	friend_colleague: "onboarding.attribution.channel.friend_colleague",
	github: "onboarding.attribution.channel.github",
	hacker_news: "onboarding.attribution.channel.hacker_news",
	reddit: "onboarding.attribution.channel.reddit",
	search: "onboarding.attribution.channel.search",
	ai_assistant: "onboarding.attribution.channel.ai_assistant",
	newsletter_blog: "onboarding.attribution.channel.newsletter_blog",
	conference_event: "onboarding.attribution.channel.conference_event",
	other: "onboarding.attribution.channel.other",
};

/**
 * Optional first-run "How did you hear about Orchestrator?" step, between the
 * welcome actions and GitHub sign-in. Shown only while ~/.ao/app-state.json
 * says "pending", which only a first-run marker does, so existing installs never
 * see it. One click on a channel answers; Skip dismisses. Either way it is
 * gone for good. The answer is stored even when analytics are off; only the
 * telemetry is gated (captureRendererEvent honors the opt-out).
 */
export function AttributionQuestion({ className }: { className?: string }) {
	const { t } = useTranslation();
	const titleId = useId();
	const queryClient = useQueryClient();
	const query = useQuery({
		queryKey: acquisitionChannelQueryKey,
		queryFn: (): Promise<AcquisitionChannelState> => aoBridge.appState.getAcquisitionChannel(),
		staleTime: Infinity,
		retry: false,
		throwOnError: false,
	});
	const visible = query.data === "pending";
	useOnboardingStep("attribution", visible);

	const answer = (value: AcquisitionAnswer) => {
		// Reported before the step hides, so hiding is not also counted as completed.
		if (value === "skipped") reportOnboardingStep("attribution", "skipped");
		queryClient.setQueryData<AcquisitionChannelState>(acquisitionChannelQueryKey, value);
		void aoBridge.appState
			.recordAcquisitionChannel(value)
			.catch(() => false)
			.then((recorded) => {
				// Only the call that moved the marker out of "pending" reports, so
				// the event is sent at most once per install.
				if (!recorded) return;
				void captureRendererEvent(
					"ao.onboarding.attribution_answered",
					value === "skipped" ? { skipped: true } : { channel: value, skipped: false },
				);
			});
	};

	if (!visible) return null;
	return (
		<section aria-labelledby={titleId} className={cn("space-y-3 px-3", className)} data-testid="attribution-question">
			<div className="flex items-baseline justify-between gap-4">
				<h2 className="text-sm font-medium text-foreground" id={titleId}>
					{t("onboarding.attribution.title")}
				</h2>
				{/* Pulled flush so the label lines up with the Star Us link above. */}
				<Button className="-mr-2.5 text-muted-foreground" onClick={() => answer("skipped")} size="sm" variant="ghost">
					{t("onboarding.attribution.skip")}
				</Button>
			</div>
			<div aria-labelledby={titleId} className="flex flex-wrap gap-2" role="group">
				{ACQUISITION_CHANNELS.map((channel) => (
					<Button key={channel} onClick={() => answer(channel)} size="sm" variant="outline">
						{t(CHANNEL_LABEL_KEYS[channel])}
					</Button>
				))}
			</div>
		</section>
	);
}
