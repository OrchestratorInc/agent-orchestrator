import { describe, expect, it } from "vitest";
import { WEEKDAY_CODES, buildRRuleFromSchedule, describeSchedule, parseHourMinute, parseTimeValue, scheduleFieldsFromRRule } from "./automation-schedule";

describe("automation-schedule", () => {
	it("roundtrips every weekday the form can choose", () => {
		for (const weekday of WEEKDAY_CODES) {
			const rrule = `FREQ=WEEKLY;BYDAY=${weekday};BYHOUR=9;BYMINUTE=15;BYSECOND=0`;
			const fields = scheduleFieldsFromRRule(rrule);
			expect(fields.preset).toBe("weekly");
			expect(fields.weekday).toBe(weekday);
			expect(buildRRuleFromSchedule(fields)).toBe(rrule);
			expect(
				buildRRuleFromSchedule({
					...fields,
					preset: "custom",
					customFrequency: "weekly",
					hour: "9",
					minute: "15",
				}),
			).toBe(rrule);
		}
	});

	it("maps weekly RRULE to preset weekly with weekday and time", () => {
		const rrule = "FREQ=WEEKLY;BYDAY=FR;BYHOUR=14;BYMINUTE=30;BYSECOND=0";
		const fields = scheduleFieldsFromRRule(rrule);
		expect(fields.preset).toBe("weekly");
		expect(fields.weekday).toBe("FR");
		expect(fields.time).toBe("14:30");
		expect(fields.legacyRaw).toBeNull();
		expect(buildRRuleFromSchedule(fields)).toBe(rrule);
	});

	it("builds weekly RRULE from custom weekly fields", () => {
		expect(
			buildRRuleFromSchedule({
				preset: "custom",
				time: "09:00",
				customFrequency: "weekly",
				monthDay: "1",
				weekday: "WE",
				hour: "16",
				minute: "45",
				legacyRaw: null,
			}),
		).toBe("FREQ=WEEKLY;BYDAY=WE;BYHOUR=16;BYMINUTE=45;BYSECOND=0");
	});

	it("roundtrips daily and monthly simple rules", () => {
		const daily = "FREQ=DAILY;BYHOUR=9;BYMINUTE=0;BYSECOND=0";
		expect(buildRRuleFromSchedule(scheduleFieldsFromRRule(daily))).toBe(daily);

		const monthly = "FREQ=MONTHLY;BYMONTHDAY=15;BYHOUR=8;BYMINUTE=15;BYSECOND=0";
		const monthlyFields = scheduleFieldsFromRRule(monthly);
		expect(monthlyFields.preset).toBe("custom");
		expect(monthlyFields.customFrequency).toBe("monthly");
		expect(monthlyFields.monthDay).toBe("15");
		expect(buildRRuleFromSchedule(monthlyFields)).toBe(monthly);
	});
});

it("round trips and edits weekday schedules without reverting to monthly", () => {
	const fields = scheduleFieldsFromRRule("FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;BYHOUR=9;BYMINUTE=0;BYSECOND=0");
	expect(fields).toMatchObject({ preset: "custom", customFrequency: "weekdays", hour: "9", minute: "0", legacyRaw: null });
	expect(buildRRuleFromSchedule({ ...fields, hour: "10", minute: "30" })).toBe("FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;BYHOUR=10;BYMINUTE=30;BYSECOND=0");
});

it.each([
	["FREQ=DAILY;BYHOUR=9;BYMINUTE=15;BYSECOND=0", "daily", "daily"],
	["FREQ=WEEKLY;BYDAY=FR;BYHOUR=16;BYMINUTE=0;BYSECOND=0", "weekly", "weekly"],
	["FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;BYHOUR=9;BYMINUTE=0;BYSECOND=0", "custom", "weekdays"],
])("shows saved starter schedule %s while preserving its daemon anchor", (rrule, preset, frequency) => {
	const saved = `DTSTART;TZID=America/New_York:20261015T080000\nRRULE:${rrule}`;
	const fields = scheduleFieldsFromRRule(saved);
	expect(fields.preset).toBe(preset);
	expect(fields.customFrequency).toBe(frequency);
	expect(buildRRuleFromSchedule(fields)).toBe(saved);
	if (frequency === "weekdays") {
		expect(buildRRuleFromSchedule({ ...fields, legacyRaw: null, hour: "10" })).toBe("FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;BYHOUR=10;BYMINUTE=0;BYSECOND=0");
	}
});

it.each([
	"FREQ=DAILY;BYHOUR=9,17;BYMINUTE=0;BYSECOND=0",
	"FREQ=DAILY;BYHOUR=9;BYMINUTE=0,30;BYSECOND=0",
	"FREQ=WEEKLY;BYDAY=FR;BYHOUR=9,17;BYMINUTE=0;BYSECOND=0",
	"FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;BYHOUR=9;BYMINUTE=0,30;BYSECOND=0",
	"FREQ=MONTHLY;BYMONTHDAY=1,15;BYHOUR=9;BYMINUTE=0;BYSECOND=0",
	"FREQ=MONTHLY;BYMONTHDAY=-1;BYHOUR=9;BYMINUTE=0;BYSECOND=0",
	"FREQ=MONTHLY;BYMONTHDAY=1;BYHOUR=9,17;BYMINUTE=0;BYSECOND=0",
])("keeps unsupported recurrence %s in the raw-rule fallback", (rule) => {
	for (const raw of [rule, `DTSTART:20261015T080000Z\nRRULE:${rule}`]) {
		const fields = scheduleFieldsFromRRule(raw);
		expect(fields).toMatchObject({ preset: "custom", monthDay: "1", legacyRaw: raw });
		expect(parseTimeValue(fields.time)).not.toBeNull();
		expect(parseHourMinute(fields.hour, fields.minute)).not.toBeNull();
		expect(buildRRuleFromSchedule(fields)).toBe(raw);
	}
});

describe("describeSchedule", () => {
	const nine = { hour: 9, minute: 0 };
	it.each([
		["FREQ=DAILY;BYHOUR=9;BYMINUTE=0;BYSECOND=0", { kind: "daily", time: nine }],
		["FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;BYHOUR=9;BYMINUTE=0;BYSECOND=0", { kind: "weekdays", time: nine }],
		["FREQ=WEEKLY;BYDAY=FR,MO,WE;BYHOUR=9;BYMINUTE=0", { kind: "weekly", days: ["MO", "WE", "FR"], time: nine }],
		["FREQ=WEEKLY;BYDAY=SU;BYHOUR=9;BYMINUTE=0", { kind: "weekly", days: ["SU"], time: nine }],
		["FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR,SA,SU;BYHOUR=9;BYMINUTE=0", { kind: "daily", time: nine }],
		["FREQ=MONTHLY;BYMONTHDAY=15;BYHOUR=9;BYMINUTE=0", { kind: "monthly", day: 15, time: nine }],
		["DTSTART;TZID=Asia/Kolkata:20261001T183600\nRRULE:FREQ=DAILY;BYHOUR=9;BYMINUTE=0", { kind: "daily", time: nine }],
		["FREQ=DAILY", { kind: "daily", time: null }],
		["FREQ=HOURLY", { kind: "other", frequency: "hourly", time: null }],
		["FREQ=DAILY;INTERVAL=2;BYHOUR=9;BYMINUTE=0", { kind: "other", frequency: "daily", time: nine }],
		["FREQ=WEEKLY;BYDAY=1MO;BYHOUR=9;BYMINUTE=0", { kind: "other", frequency: "weekly", time: nine }],
		["FREQ=MONTHLY;BYMONTHDAY=-1", { kind: "other", frequency: "monthly", time: null }],
	])("summarizes %s", (rule, expected) => {
		expect(describeSchedule(rule)).toEqual(expected);
	});
});
