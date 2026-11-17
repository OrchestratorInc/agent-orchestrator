package chattraffic

import "slices"

// Outcome statuses and exit codes.
const (
	StatusPass         = "pass"
	StatusFail         = "fail"
	StatusIncomparable = "incomparable"
	// StatusUnverified is a comparison that met every limit but measured a
	// renderer the client simulator does not model. It is never a pass.
	StatusUnverified = "unverified"

	ExitPass         = 0
	ExitFail         = 1
	ExitIncomparable = 2
)

// Reason severities.
const (
	SeverityInfo         = "info"
	SeverityFail         = "fail"
	SeverityIncomparable = "incomparable"
	SeverityUnverified   = "unverified"
)

// Options are the trusted inputs a comparison checks the runs against. They
// come from the caller, never from a run.
type Options struct {
	// CeilingsSha256 is the digest of the exact ceilings bytes the caller
	// decoded. Each run must name the same file.
	CeilingsSha256 string
	// ExpectBaselineRef and ExpectCandidateRef, when set, are the commits the
	// caller asked to measure.
	ExpectBaselineRef  string
	ExpectCandidateRef string
	// AllowClientChange compares runs whose renderer source differs from what
	// the client simulator models, to show their numbers. Such a comparison is
	// reported as unverified (exit 2) when nothing failed: never as a pass.
	AllowClientChange bool
}

// Report is the machine-readable comparison result.
type Report struct {
	SchemaVersion       string      `json:"schemaVersion"`
	Status              string      `json:"status"`
	ExitCode            int         `json:"exitCode"`
	Reasons             []Reason    `json:"reasons"`
	Baseline            Provenance  `json:"baseline"`
	Candidate           Provenance  `json:"candidate"`
	CeilingsSha256      string      `json:"ceilingsSha256"`
	FixturesSha256      string      `json:"fixturesSha256"`
	MaxIncreasePermille int64       `json:"maxIncreasePermille"`
	Rows                []ReportRow `json:"rows"`
}

// Reason explains a status with a fixed code and allowlisted identifiers.
type Reason struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Scenario string `json:"scenario,omitempty"`
	Metric   string `json:"metric,omitempty"`
}

// Provenance repeats the identity fields of one run.
type Provenance struct {
	Side                  string       `json:"side"`
	SourceRef             string       `json:"sourceRef"`
	Dirty                 bool         `json:"dirty"`
	DirtyDiffSha256       string       `json:"dirtyDiffSha256"`
	GoVersion             string       `json:"goVersion"`
	GOOS                  string       `json:"goos"`
	GOARCH                string       `json:"goarch"`
	HarnessSha256         string       `json:"harnessSha256"`
	FixturesSha256        string       `json:"fixturesSha256"`
	ClientSourceSha256    string       `json:"clientSourceSha256"`
	SimulatorClientSha256 string       `json:"simulatorClientSha256"`
	ClientPolicy          ClientPolicy `json:"clientPolicy"`
	Nonce                 string       `json:"nonce"`
}

// ReportRow compares one scenario.
type ReportRow struct {
	Provider      string      `json:"provider"`
	Phase         string      `json:"phase"`
	Transport     string      `json:"transport"`
	Status        string      `json:"status"`
	SemanticMatch bool        `json:"semanticMatch"`
	Metrics       []MetricRow `json:"metrics"`
}

// MetricRow compares one metric. DeltaPermille is meaningful only when
// RelativeAvailable: a zero baseline or an incomparable pair has no relative
// change, and none is invented.
type MetricRow struct {
	Name              string `json:"name"`
	Baseline          int64  `json:"baseline"`
	Candidate         int64  `json:"candidate"`
	Ceiling           int64  `json:"ceiling"`
	Allowed           int64  `json:"allowed"`
	Delta             int64  `json:"delta"`
	DeltaPermille     int64  `json:"deltaPermille"`
	RelativeAvailable bool   `json:"relativeAvailable"`
	Status            string `json:"status"`
}

// Metric statuses.
const (
	MetricOK            = "ok"
	MetricNotApplicable = "n/a"
	MetricOverCeiling   = "over-ceiling"
	MetricRegressed     = "regressed"
	MetricOverBoth      = "over-ceiling-and-regressed"
	MetricFromZero      = "new-traffic"
)

func provenance(r Run) Provenance {
	return Provenance{
		Side: r.Side, SourceRef: r.SourceRef, Dirty: r.Dirty, DirtyDiffSha256: r.DirtyDiffSha256,
		GoVersion: r.GoVersion, GOOS: r.GOOS, GOARCH: r.GOARCH, HarnessSha256: r.HarnessSha256,
		FixturesSha256: r.FixturesSha256, ClientSourceSha256: r.ClientSourceSha256,
		SimulatorClientSha256: r.SimulatorClientSha256, ClientPolicy: r.ClientPolicy, Nonce: r.Nonce,
	}
}

// Compare judges a candidate run against a baseline run and the reviewed
// ceilings. Both runs and the ceilings must already have been validated by
// DecodeRun and DecodeCeilings. Ceilings come only from the ceilings argument.
func Compare(baseline, candidate Run, ceilings Ceilings, opts Options) Report {
	report := Report{
		SchemaVersion:       ReportSchema,
		Reasons:             []Reason{},
		Baseline:            provenance(baseline),
		Candidate:           provenance(candidate),
		CeilingsSha256:      opts.CeilingsSha256,
		FixturesSha256:      ceilings.FixturesSha256,
		MaxIncreasePermille: ceilings.MaxIncreasePermille,
		Rows:                []ReportRow{},
	}
	add := func(code, severity string, scenario ScenarioKey, metric string) {
		reason := Reason{Code: code, Severity: severity, Metric: metric}
		if scenario != (ScenarioKey{}) {
			reason.Scenario = scenario.String()
		}
		report.Reasons = append(report.Reasons, reason)
	}
	global := func(code string) { add(code, SeverityIncomparable, ScenarioKey{}, "") }

	if baseline.Side != "baseline" || candidate.Side != "candidate" {
		global("side-mismatch")
	}
	if opts.ExpectBaselineRef != "" && baseline.SourceRef != opts.ExpectBaselineRef {
		global("baseline-ref-mismatch")
	}
	if opts.ExpectCandidateRef != "" && candidate.SourceRef != opts.ExpectCandidateRef {
		global("candidate-ref-mismatch")
	}
	if baseline.Dirty {
		global("baseline-dirty")
	}
	if baseline.GoVersion != candidate.GoVersion || baseline.GOOS != candidate.GOOS || baseline.GOARCH != candidate.GOARCH {
		global("toolchain-mismatch")
	}
	// Both sides run the same harness, and so the same simulator. Its renderer
	// revisions may differ when each is one the simulator was checked against.
	if baseline.HarnessSha256 != candidate.HarnessSha256 {
		global("harness-mismatch")
	}
	if baseline.FixturesSha256 != candidate.FixturesSha256 || candidate.FixturesSha256 != ceilings.FixturesSha256 {
		global("fixtures-mismatch")
	}
	if !hex64Pattern.MatchString(opts.CeilingsSha256) ||
		baseline.CeilingsSha256 != opts.CeilingsSha256 || candidate.CeilingsSha256 != opts.CeilingsSha256 {
		global("ceilings-mismatch")
	}
	clientChanged := false
	for _, run := range []Run{baseline, candidate} {
		if run.ClientSourceSha256 != run.SimulatorClientSha256 {
			clientChanged = true
		}
	}
	if clientChanged {
		if opts.AllowClientChange {
			add("client-source-unmodeled-allowed", SeverityUnverified, ScenarioKey{}, "")
		} else {
			global("client-source-unmodeled")
		}
	}
	if baseline.ClientPolicy != candidate.ClientPolicy {
		add("client-policy-changed", SeverityInfo, ScenarioKey{}, "")
	}
	runsComparable := !hasSeverity(report.Reasons, SeverityIncomparable)

	baseRows := scenarioMap(baseline.Scenarios)
	candRows := scenarioMap(candidate.Scenarios)
	ceilRows := map[ScenarioKey]CeilingRow{}
	for _, row := range ceilings.Rows {
		ceilRows[row.Key()] = row
	}
	keys := orderedKeys(baseRows, candRows, ceilRows)
	for _, key := range keys {
		base, inBase := baseRows[key]
		cand, inCand := candRows[key]
		ceiling, inCeilings := ceilRows[key]
		switch {
		case !inBase:
			add("scenario-missing-in-baseline", SeverityIncomparable, key, "")
			continue
		case !inCand:
			add("scenario-missing-in-candidate", SeverityIncomparable, key, "")
			continue
		case !inCeilings:
			add("ceiling-missing", SeverityIncomparable, key, "")
			continue
		}
		row := ReportRow{Provider: key.Provider, Phase: key.Phase, Transport: key.Transport, Status: StatusPass}
		rowComparable := runsComparable
		if base.State.ExpectedDigest != cand.State.ExpectedDigest {
			add("expected-state-mismatch", SeverityIncomparable, key, "")
			rowComparable = false
		}
		if base.State.ClientDigest != base.State.ExpectedDigest || base.State.ServerDigest != base.State.ExpectedDigest {
			add("baseline-state-mismatch", SeverityIncomparable, key, "")
			rowComparable = false
		}
		row.SemanticMatch = cand.State.ClientDigest == cand.State.ExpectedDigest &&
			cand.State.ServerDigest == cand.State.ExpectedDigest &&
			cand.State.Messages == base.State.Messages && cand.State.Activities == base.State.Activities &&
			cand.State.Turns == base.State.Turns
		if !row.SemanticMatch {
			add("candidate-state-mismatch", SeverityFail, key, "")
			row.Status = StatusFail
		}
		baseValues, candValues, maxValues := base.Metrics.Values(), cand.Metrics.Values(), ceiling.Max.Values()
		for i, name := range MetricNames {
			metric := judgeMetric(name, baseValues[i], candValues[i], maxValues[i], ceilings.MaxIncreasePermille, rowComparable)
			switch metric.Status {
			case MetricOverCeiling, MetricRegressed, MetricOverBoth, MetricFromZero:
				add(metric.Status, SeverityFail, key, name)
				row.Status = StatusFail
			}
			row.Metrics = append(row.Metrics, metric)
		}
		if overAnyCeiling(base.Metrics, ceiling.Max) {
			add("baseline-over-ceiling", SeverityInfo, key, "")
		}
		report.Rows = append(report.Rows, row)
	}

	switch {
	case hasSeverity(report.Reasons, SeverityIncomparable):
		report.Status, report.ExitCode = StatusIncomparable, ExitIncomparable
	case hasSeverity(report.Reasons, SeverityFail):
		report.Status, report.ExitCode = StatusFail, ExitFail
	case hasSeverity(report.Reasons, SeverityUnverified):
		report.Status, report.ExitCode = StatusUnverified, ExitIncomparable
	default:
		report.Status, report.ExitCode = StatusPass, ExitPass
	}
	return report
}

// judgeMetric applies the absolute ceiling always and the relative cap only
// when the pair is comparable. Integer arithmetic only: values are bounded by
// MaxMetric, so the products cannot overflow.
func judgeMetric(name string, base, cand, ceiling, permille int64, pairComparable bool) MetricRow {
	metric := MetricRow{Name: name, Baseline: base, Candidate: cand, Ceiling: ceiling, Allowed: ceiling, Delta: cand - base}
	over := cand > ceiling
	regressed := false
	if pairComparable && base > 0 {
		metric.RelativeAvailable = true
		metric.DeltaPermille = (cand - base) * 1000 / base
		allowed := base + base*permille/1000
		metric.Allowed = min(ceiling, allowed)
		regressed = cand > allowed
	}
	switch {
	case pairComparable && base == 0 && cand == 0:
		metric.Status = MetricNotApplicable
	case pairComparable && base == 0 && cand > 0:
		metric.Status = MetricFromZero
	case over && regressed:
		metric.Status = MetricOverBoth
	case over:
		metric.Status = MetricOverCeiling
	case regressed:
		metric.Status = MetricRegressed
	default:
		metric.Status = MetricOK
	}
	return metric
}

func overAnyCeiling(m, ceiling Metrics) bool {
	values, limits := m.Values(), ceiling.Values()
	for i := range values {
		if values[i] > limits[i] {
			return true
		}
	}
	return false
}

func hasSeverity(reasons []Reason, severity string) bool {
	return slices.ContainsFunc(reasons, func(r Reason) bool { return r.Severity == severity })
}

func scenarioMap(scenarios []Scenario) map[ScenarioKey]Scenario {
	rows := make(map[ScenarioKey]Scenario, len(scenarios))
	for _, scenario := range scenarios {
		rows[scenario.Key()] = scenario
	}
	return rows
}

// orderedKeys lists every key any input names, in allowlist order, so a
// report is stable and a key missing on one side is still reported.
func orderedKeys(base, cand map[ScenarioKey]Scenario, ceilings map[ScenarioKey]CeilingRow) []ScenarioKey {
	present := map[ScenarioKey]bool{}
	for key := range base {
		present[key] = true
	}
	for key := range cand {
		present[key] = true
	}
	for key := range ceilings {
		present[key] = true
	}
	var keys []ScenarioKey
	for _, provider := range Providers {
		for _, phase := range Phases {
			for _, transport := range Transports {
				key := ScenarioKey{provider, phase, transport}
				if present[key] {
					keys = append(keys, key)
				}
			}
		}
	}
	return keys
}
