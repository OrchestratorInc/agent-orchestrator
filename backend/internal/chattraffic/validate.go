package chattraffic

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
)

var (
	hex32Pattern     = regexp.MustCompile(`^[0-9a-f]{32}$`)
	hex40Pattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	hex64Pattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	goVersionPattern = regexp.MustCompile(`^go1\.\d{1,3}(\.\d{1,3})?((rc|beta)\d{1,2})?$`)
	platformPattern  = regexp.MustCompile(`^[a-z0-9]{1,16}$`)
)

// DecodeRun strictly decodes and validates one run artifact.
func DecodeRun(data []byte) (Run, error) {
	var run Run
	if err := decodeStrict(data, &run); err != nil {
		return Run{}, err
	}
	if err := run.Validate(); err != nil {
		return Run{}, err
	}
	return run, nil
}

// DecodeCeilings strictly decodes and validates a ceilings file.
func DecodeCeilings(data []byte) (Ceilings, error) {
	var ceilings Ceilings
	if err := decodeStrict(data, &ceilings); err != nil {
		return Ceilings{}, err
	}
	if err := ceilings.Validate(); err != nil {
		return Ceilings{}, err
	}
	return ceilings, nil
}

// Validate checks every field of a run. It does not judge the measurements;
// Compare does.
func (r Run) Validate() error {
	if r.SchemaVersion != RunSchema {
		return fmt.Errorf("schemaVersion must be %q", RunSchema)
	}
	if !slices.Contains(Sides, r.Side) {
		return errors.New("side is not baseline or candidate")
	}
	if !hex40Pattern.MatchString(r.SourceRef) {
		return errors.New("sourceRef must be a 40-character lowercase hex commit")
	}
	if r.Dirty != hex64Pattern.MatchString(r.DirtyDiffSha256) {
		return errors.New("dirtyDiffSha256 must be a sha256 when dirty and empty when clean")
	}
	if !r.Dirty && r.DirtyDiffSha256 != "" {
		return errors.New("dirtyDiffSha256 must be empty when clean")
	}
	if !goVersionPattern.MatchString(r.GoVersion) {
		return errors.New("goVersion is not a released Go version")
	}
	if !platformPattern.MatchString(r.GOOS) || !platformPattern.MatchString(r.GOARCH) {
		return errors.New("goos and goarch must be lowercase identifiers")
	}
	for name, digest := range map[string]string{
		"harnessSha256": r.HarnessSha256, "fixturesSha256": r.FixturesSha256,
		"ceilingsSha256": r.CeilingsSha256, "clientSourceSha256": r.ClientSourceSha256,
		"simulatorClientSha256": r.SimulatorClientSha256,
	} {
		if !hex64Pattern.MatchString(digest) {
			return fmt.Errorf("%s must be a lowercase sha256", name)
		}
	}
	if !hex32Pattern.MatchString(r.Nonce) {
		return errors.New("nonce must be 32 lowercase hex characters")
	}
	if err := r.ClientPolicy.validate(); err != nil {
		return err
	}
	if len(r.Scenarios) == 0 {
		return errors.New("run has no scenarios")
	}
	seen := map[ScenarioKey]bool{}
	for _, scenario := range r.Scenarios {
		if err := validateKey(scenario.Key()); err != nil {
			return err
		}
		if seen[scenario.Key()] {
			return fmt.Errorf("scenario %s appears twice", scenario.Key())
		}
		seen[scenario.Key()] = true
		if err := scenario.validate(); err != nil {
			return fmt.Errorf("scenario %s: %w", scenario.Key(), err)
		}
	}
	return nil
}

func (p ClientPolicy) validate() error {
	for name, value := range map[string]int64{
		"localPageSize": p.LocalPageSize, "localOlderPageSize": p.LocalOlderPageSize, "remoteLivePageSize": p.RemoteLivePageSize,
		"remoteOlderPageSize": p.RemoteOlderPageSize, "remoteOutputLimit": p.RemoteOutputLimit,
		"invalidateWindowMs": p.InvalidateWindowMs,
	} {
		if value == 0 {
			return fmt.Errorf("clientPolicy.%s must be positive", name)
		}
	}
	return nil
}

func validateKey(key ScenarioKey) error {
	switch {
	case !slices.Contains(Providers, key.Provider):
		return errors.New("scenario provider is not allowlisted")
	case !slices.Contains(Phases, key.Phase):
		return errors.New("scenario phase is not allowlisted")
	case !slices.Contains(Transports, key.Transport):
		return errors.New("scenario transport is not allowlisted")
	case key.Phase == "expand-output" && key.Transport != "remote-lan":
		return errors.New("expand-output exists only for remote-lan, the transport that elides output")
	}
	return nil
}

func (s Scenario) validate() error {
	m := s.Metrics
	if m.WireBytesIn != m.HeaderBytesIn+m.FramingBytesIn+m.BodyEncodedBytes+m.SSEBytes {
		return errors.New("wireBytesIn must equal headerBytesIn + framingBytesIn + bodyEncodedBytes + sseBytes")
	}
	if m.Requests == 0 || m.HeaderBytesIn == 0 || m.BodyEncodedBytes == 0 || m.BodyDecodedBytes == 0 || m.WireBytesOut == 0 {
		return errors.New("a scenario must issue requests and receive headers and bodies")
	}
	if s.Transport == "local" && m.BodyEncodedBytes != m.BodyDecodedBytes {
		return errors.New("the loopback listener never compresses, so encoded and decoded bodies must match")
	}
	if s.Phase == "live-turn" {
		if m.SSEFrames == 0 || m.SSEBytes == 0 || m.CDCRows == 0 {
			return errors.New("a live turn must produce change rows and event-stream frames")
		}
	} else if m.SSEFrames != 0 || m.SSEBytes != 0 || m.CDCRows != 0 {
		return errors.New("only a live turn reads the event stream")
	}
	st := s.State
	for name, digest := range map[string]string{
		"expectedDigest": st.ExpectedDigest, "clientDigest": st.ClientDigest, "serverDigest": st.ServerDigest,
	} {
		if !hex64Pattern.MatchString(digest) {
			return fmt.Errorf("state.%s must be a lowercase sha256", name)
		}
	}
	if s.Phase == "expand-output" {
		if st.Turns == 0 || st.Activities == 0 {
			return errors.New("an expansion must recover activity output")
		}
	} else if st.Turns == 0 || st.Messages == 0 {
		return errors.New("state must hold turns and messages")
	}
	return nil
}

// Validate checks a ceilings file.
func (c Ceilings) Validate() error {
	if c.SchemaVersion != CeilingsSchema {
		return fmt.Errorf("schemaVersion must be %q", CeilingsSchema)
	}
	if !hex64Pattern.MatchString(c.FixturesSha256) {
		return errors.New("fixturesSha256 must be a lowercase sha256")
	}
	if c.MaxIncreasePermille > 1000 {
		return errors.New("maxIncreasePermille must be at most 1000")
	}
	if len(c.Rows) == 0 {
		return errors.New("ceilings have no rows")
	}
	seen := map[ScenarioKey]bool{}
	for _, row := range c.Rows {
		if err := validateKey(row.Key()); err != nil {
			return err
		}
		if seen[row.Key()] {
			return fmt.Errorf("ceiling %s appears twice", row.Key())
		}
		seen[row.Key()] = true
	}
	return nil
}
