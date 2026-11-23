import { useCallback, useEffect, useState } from "react";
import { StyleSheet, Text, View } from "react-native";
import type { Theme } from "../theme";
import { useThemedStyles } from "../ThemeProvider";
import { gutter, radius, space, type } from "../tokens";
import { BrowserApprovalCard } from "./BrowserApprovalCard";
import type { ApprovalCard, ApprovalDecision, DecisionResult } from "./types";

/**
 * The oldest approval waiting in this session, over the screen. `subscribe`
 * feeds the live list; a card decided elsewhere leaves with the next read.
 * When a notification opened this for an approval that already ended, it
 * says "Already decided" instead of showing nothing.
 */
export function ApprovalOverlay({ subscribe, decide, openedFor, bottom }: {
	subscribe: (onChange: (cards: ApprovalCard[]) => void) => () => void;
	decide: (id: string, decision: ApprovalDecision) => Promise<DecisionResult>;
	openedFor?: string;
	/** Floats this far above the bottom; inline when absent. */
	bottom?: number;
}) {
	const s = useThemedStyles(makeStyles);
	const [cards, setCards] = useState<ApprovalCard[] | null>(null);
	const [gone, setGone] = useState<string | null>(null);
	useEffect(() => subscribe(setCards), [subscribe]);
	useEffect(() => {
		if (!openedFor || cards === null) return;
		if (!cards.some((c) => c.id === openedFor)) setGone(openedFor);
	}, [cards === null, openedFor]); // eslint-disable-line react-hooks/exhaustive-deps
	useEffect(() => {
		if (!gone) return;
		const timer = setTimeout(() => setGone(null), 4_000);
		return () => clearTimeout(timer);
	}, [gone]);
	const onDecide = useCallback(async (id: string, decision: ApprovalDecision) => {
		const result = await decide(id, decision);
		// Drop it now rather than wait for the next read.
		if (result === "decided") setCards((list) => list?.filter((c) => c.id !== id) ?? null);
		return result;
	}, [decide]);

	const card = cards?.[0];
	if (!card && !gone) return null;
	return <View pointerEvents="box-none" style={bottom === undefined ? s.inline : [s.wrap, { bottom }]}>
		{card
			? <BrowserApprovalCard key={card.id} card={card} onDecide={(decision) => onDecide(card.id, decision)} />
			: <View accessibilityRole="alert" style={s.notice}><Text style={s.noticeText}>Already decided</Text></View>}
	</View>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	wrap: { position: "absolute", left: 0, right: 0 },
	inline: { paddingVertical: space.xs },
	notice: { marginHorizontal: gutter, padding: space.md, borderRadius: radius.lg, borderCurve: "continuous", backgroundColor: t.bgElevated },
	noticeText: { ...type.subheadline, color: t.textSecondary },
});
