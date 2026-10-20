package triage

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

func sameOrigin(a, b Origin) bool {
	return a.HostID == b.HostID && a.SessionID == b.SessionID && a.Surface == b.Surface
}

func (h *host) prepare(origin *Origin) (Run, error) {
	var source Origin
	if origin != nil {
		source = *origin
	}
	if active := h.current(""); active != nil {
		if active.ctx.Err() == nil {
			run := active.snapshot()
			if !sameOrigin(source, run.Request.Origin) {
				return Run{}, errRunOwned
			}
			return run, nil
		}
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-active.done:
		case <-h.ctx.Done():
			return Run{}, h.ctx.Err()
		case <-timer.C:
			return Run{}, errors.New("the previous investigation is still stopping")
		}
	}
	now := h.deps.Now().UTC()
	run := Run{ID: uuid.NewString(), Version: ProtocolVersion, CreatedAt: now, UpdatedAt: now, Preparation: true, Request: StartRequest{Origin: source}, State: "collecting", Activity: "Collecting diagnostics while you choose an agent.", Messages: []Message{}}
	err := h.store.save(run)
	if err == nil {
		run, err = h.store.get(run.ID)
	}
	if err != nil {
		return Run{}, err
	}
	i := h.newInvestigation(run)
	go i.prepare()
	return i.snapshot(), nil
}

func (h *host) start(request *StartRequest) (Run, error) {
	if !h.handoffMu.TryLock() {
		return Run{}, errors.New("ordinary session handoff is still being prepared")
	}
	defer h.handoffMu.Unlock()
	if request == nil || len(request.Observation) > maxReplyBytes {
		return Run{}, errors.New("start requires an observation of at most 8 KiB")
	}
	frozen := *request
	selection, err := h.selection(frozen.Selection)
	if err != nil {
		return Run{}, err
	}
	frozen.Selection = selection
	i := h.current(frozen.PreparationID)
	if frozen.PreparationID == "" {
		run, err := h.prepare(&frozen.Origin)
		if err != nil {
			return Run{}, err
		}
		frozen.PreparationID = run.ID
		i = h.current(run.ID)
	} else if i == nil {
		if h.current("") != nil {
			return Run{}, errRunOwned
		}
		run, err := h.store.get(frozen.PreparationID)
		if err != nil {
			return Run{}, err
		}
		if !knownUnsentSession(run, frozen) {
			return Run{}, errors.New("the preparation is unavailable or ordinary session creation may already have started")
		}
		run.Execution, run.Preparation, run.Launch = "", true, nil
		i = h.newInvestigation(run)
		go i.prepare()
	}
	if i == nil || i.ctx.Err() != nil {
		return Run{}, errors.New("the diagnostic preparation was cancelled")
	}
	run := i.snapshot()
	if !sameOrigin(frozen.Origin, run.Request.Origin) || frozen.PreparationID != run.ID {
		return Run{}, errors.New("the report context changed; open Report a Problem again")
	}
	if !run.Preparation {
		if sameHandoffRequest(frozen, run.Request) {
			return run, nil
		}
		return Run{}, errRunOwned
	}
	if run.Execution == "session" {
		return Run{}, errors.New("this preparation is reserved for an ordinary session")
	}
	frozen.Origin = run.Request.Origin
	err = i.commitIf(func(run *Run) bool {
		if i.ctx.Err() != nil || !run.Preparation || run.Execution == "session" {
			return false
		}
		run.Preparation, run.Request = false, frozen
		run.State, run.Activity = "starting", "Starting the selected agent."
		if frozen.Observation != "" {
			run.Messages = append(run.Messages, Message{ID: newMessageID(len(run.Messages) + 1), Role: "user", Text: frozen.Observation, At: h.deps.Now().UTC()})
		}
		return true
	})
	if err != nil {
		return Run{}, err
	}
	i.activation <- struct{}{}
	return i.snapshot(), nil
}

func (i *investigation) prepare() {
	defer i.finish()
	if err := i.collectEvidence(); err != nil {
		if i.ctx.Err() == nil {
			i.fail(err)
		}
		return
	}
	if err := i.commit(func(run *Run) {
		if run.Preparation {
			run.State, run.Activity = "prepared", "Diagnostics are ready. Choose an agent to investigate."
		}
	}); err != nil {
		return
	}
	select {
	case <-i.ctx.Done():
		return
	case <-i.activation:
	}
	if i.snapshot().Execution == "session" {
		if err := i.stageSessionLaunch(); err != nil && i.ctx.Err() == nil {
			i.fail(err)
		}
	} else {
		i.runProvider()
	}
}

func (i *investigation) collectEvidence() error {
	var evidence []Evidence
	if i.host.deps.Collect != nil {
		collected, err := i.host.deps.Collect(i.ctx, i.snapshot().Request.Origin)
		evidence = collected
		if err != nil {
			evidence = append(evidence, Evidence{ID: "collection-error", Kind: "collection", Source: "local collector", CollectedAt: i.host.deps.Now().UTC(), Status: "unavailable", Text: err.Error()})
		}
	}
	if err := i.ctx.Err(); err != nil {
		return err
	}
	return i.commit(func(run *Run) { run.Evidence = append(run.Evidence, evidence...) })
}
