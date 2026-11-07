import { useCallback, useEffect, useRef, useState } from "react";
import { mentionAskSteps, type ComposerMention, type MentionAskStep } from "../../lib/cloud-team-collab";

type Pending = {
	steps: MentionAskStep[];
	index: number;
	mentions: ComposerMention[];
	resolve: (answer: ComposerMention[] | null) => void;
};

/**
 * The questions a send asks before it notifies people, one bar at a time
 * above the composer: a team of more than five (Notify or Cancel), then the
 * people who can't open the session (Share and send, or Send without them).
 * Nothing is shared silently. `confirm` is the composer's hook: undefined when
 * nothing needs asking, else the mentions to send once answered, or null.
 */
export function useMentionAsk({ share, resetKey }: {
	/** Shares the session with these people at the smallest access. */
	share: (people: Extract<ComposerMention, { kind: "person" }>[]) => Promise<unknown>;
	/** A new key (another session) drops a question still open. */
	resetKey: string;
}) {
	const pending = useRef<Pending | null>(null);
	const [step, setStep] = useState<MentionAskStep>();
	const [sharing, setSharing] = useState(false);
	// Why the last share didn't go: the control plane doesn't know the person
	// (a bare 404), or it failed some other way.
	const [shareProblem, setShareProblem] = useState<"unknown-person" | "failed">();

	const finish = useCallback((answer: ComposerMention[] | null) => {
		const current = pending.current;
		pending.current = null;
		setStep(undefined);
		setSharing(false);
		setShareProblem(undefined);
		current?.resolve(answer);
	}, []);

	const advance = useCallback((mentions: ComposerMention[]) => {
		const current = pending.current;
		if (!current) return;
		current.mentions = mentions;
		current.index += 1;
		setSharing(false);
		setShareProblem(undefined);
		if (current.index >= current.steps.length) finish(mentions);
		else setStep(current.steps[current.index]);
	}, [finish]);

	const confirm = useCallback((mentions: ComposerMention[]): Promise<ComposerMention[] | null> | undefined => {
		const steps = mentionAskSteps(mentions);
		if (steps.length === 0) return undefined;
		pending.current?.resolve(null);
		return new Promise((resolve) => {
			pending.current = { steps, index: 0, mentions, resolve };
			setSharing(false);
			setShareProblem(undefined);
			setStep(steps[0]);
		});
	}, []);

	const cancel = useCallback(() => finish(null), [finish]);

	const notifyTeam = useCallback(() => {
		if (pending.current) advance(pending.current.mentions);
	}, [advance]);

	const sendWithout = useCallback(() => {
		const current = pending.current;
		const asked = current?.steps[current.index];
		if (!current || asked?.kind !== "noAccess") return;
		const left = new Set(asked.people.map((person) => person.id));
		advance(current.mentions.filter((mention) => mention.kind !== "person" || !left.has(mention.id)));
	}, [advance]);

	const shareAndSend = useCallback(async () => {
		const current = pending.current;
		const asked = current?.steps[current.index];
		if (!current || asked?.kind !== "noAccess") return;
		setSharing(true);
		setShareProblem(undefined);
		try {
			await share(asked.people);
		} catch (error) {
			if (pending.current === current) {
				setSharing(false);
				setShareProblem((error as { status?: unknown } | null)?.status === 404 ? "unknown-person" : "failed");
			}
			return;
		}
		if (pending.current === current) advance(current.mentions);
	}, [advance, share]);

	useEffect(() => () => pending.current?.resolve(null), []);
	const firstKey = useRef(resetKey);
	useEffect(() => {
		if (firstKey.current === resetKey) return;
		firstKey.current = resetKey;
		finish(null);
	}, [finish, resetKey]);

	return { step, sharing, shareProblem, confirm, cancel, notifyTeam, sendWithout, shareAndSend };
}
