package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/capability"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/scheduler"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// m3ProcessResult 只返回非秘密竞争结果；租约能力始终保留在独立进程的 SQLite 中。
type m3ProcessResult struct {
	Won       bool
	AttemptID domain.WorkflowAttemptID
}

// TestM3IndependentProcessesClaimOnce 删除 SQLite 原子 Leader/Claim 栅栏，会在独立进程之间产生两条领取或占位。
func TestM3IndependentProcessesClaimOnce(t *testing.T) {
	f := newM3Fixture(t)
	wid := m3Create(t, f, "process-race", 1)
	taskID := domain.WorkflowTaskID("process-race-task-00")
	f.m3.clock.advance(scheduler.LeaderLease + time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	type child struct {
		cmd    *exec.Cmd
		input  io.WriteCloser
		output *bufio.Scanner
		stderr bytes.Buffer
	}
	children := make([]*child, 2)
	for i := range children {
		p := &child{}
		p.cmd = exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSchedulerClaimProcessHelper$", "-test.v")
		p.cmd.Env = append(os.Environ(), "AO_M3_CLAIM_HELPER=1", "AO_M3_CLAIM_DATA="+f.dataDir, "AO_M3_CLAIM_WORKFLOW="+string(wid), "AO_M3_CLAIM_TASK="+string(taskID), "AO_M3_CLAIM_NOW="+f.m3.clock.Now().Format(time.RFC3339Nano), fmt.Sprintf("AO_M3_CLAIM_OWNER=process-%d", i))
		p.cmd.Stderr = &p.stderr
		var err error
		p.input, err = p.cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		output, err := p.cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		p.output = bufio.NewScanner(output)
		if err := p.cmd.Start(); err != nil {
			t.Fatal(err)
		}
		children[i] = p
	}
	for _, p := range children {
		ready := false
		for p.output.Scan() {
			if p.output.Text() == "M3_READY" {
				ready = true
				break
			}
		}
		if !ready {
			t.Fatalf("child did not reach SQLite barrier: %v", p.output.Err())
		}
	}
	for _, p := range children {
		if _, err := p.input.Write([]byte{1}); err != nil {
			t.Fatal(err)
		}
		if err := p.input.Close(); err != nil {
			t.Fatal(err)
		}
	}
	winners := 0
	var winner domain.WorkflowAttemptID
	for _, p := range children {
		found := false
		for p.output.Scan() {
			line := p.output.Text()
			if !strings.HasPrefix(line, "M3_RESULT ") {
				continue
			}
			var result m3ProcessResult
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "M3_RESULT ")), &result); err != nil {
				t.Fatal(err)
			}
			if found {
				t.Fatal("child returned multiple claim results")
			}
			found = true
			if result.Won {
				winners++
				winner = result.AttemptID
			}
		}
		if err := p.output.Err(); err != nil {
			t.Fatal(err)
		}
		if err := p.cmd.Wait(); err != nil {
			t.Fatalf("independent scheduler: %v %s", err, p.stderr.String())
		}
		if !found {
			t.Fatal("child omitted claim result")
		}
	}
	attempts, err := f.store.ListWorkflowAttempts(ctx, wid, taskID)
	if err != nil || winners != 1 || len(attempts) != 1 || attempts[0].ID != winner {
		t.Fatalf("process claims: winners=%d attempts=%d err=%v", winners, len(attempts), err)
	}
	ads, err := f.store.ListSchedulerAdmissions(ctx, true)
	if err != nil || len(ads) != 1 || ads[0].AttemptID != winner || ads[0].SessionID != "" {
		t.Fatalf("process admissions: %+v err=%v", ads, err)
	}
	// 子进程只 Claim；重启接管将未完成的旧占位冻结，不能冒用已退出进程的执行身份。
	f.m3.clock.advance(scheduler.LeaderLease + time.Second)
	next := newM3Control(t, f.store, f.m3.clock, "process-consumer")
	consumer := newM2FFFixture(t, m2ffPlant{store: f.store, dataDir: f.dataDir, managedDir: f.managedDir, repoA: f.repoA, repoB: f.repoB, m3: next})
	r := consumer.asOperator(http.MethodPost, consumer.bindPath(wid, winner), `{"harness":"codex","mode":"chat","prompt":"consume one admission","idempotencyKey":"process-bind"}`)
	if r.Status != http.StatusConflict || consumer.text(r.Body, "code") != "SCHEDULER_ADMISSION_CONFLICT" {
		t.Fatalf("uncertain process claim was restarted: %d %s", r.Status, r.Raw)
	}
	if len(consumer.sessionsFor()) != 0 {
		t.Fatal("recovery spawned an uncertain process claim")
	}
	ads, err = consumer.store.ListSchedulerAdmissions(ctx, true)
	if err != nil || len(ads) != 1 || ads[0].State != domain.AdmissionUncertain {
		t.Fatalf("lost uncertain occupancy: %+v err=%v", ads, err)
	}
}

// TestSchedulerClaimProcessHelper 是上述独立进程的入口，不计入 TestM2|TestM3 验收数。
// 它只抢真实数据库任期和同一任务，不创建 Driver、Manager 或任何 Provider 进程。
func TestSchedulerClaimProcessHelper(t *testing.T) {
	if os.Getenv("AO_M3_CLAIM_HELPER") != "1" {
		return
	}
	ctx := context.Background()
	st, err := sqlite.OpenPreMigrated(os.Getenv("AO_M3_CLAIM_DATA"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now, err := time.Parse(time.RFC3339Nano, os.Getenv("AO_M3_CLAIM_NOW"))
	if err != nil {
		t.Fatal(err)
	}
	clock := &m3Clock{now: now}
	resolver := capability.NewResolver(capability.WithClock(clock.Now))
	resolver.RegisterProbeFunc(domain.HarnessCodex, func(_ context.Context, req ports.CapabilityProbeRequest) (domain.HarnessCapabilitySnapshot, error) {
		c := domain.NewHarnessCapabilitySnapshot(req.Harness, domain.CapabilitySourceConfirmedRuntime, now, time.Minute)
		c.Readiness, c.AuthState, c.Surfaces = domain.CapabilityReady, domain.AgentAuthenticationAuthorized, domain.CapabilitySurfaceBoth
		return c, nil
	})
	svc := scheduler.New(scheduler.Deps{Store: st, Tasks: st, OwnerID: os.Getenv("AO_M3_CLAIM_OWNER"), Clock: clock.Now, Resources: &m3Resources{clock: clock, cpu: 20, available: 12 << 30}, Capabilities: resolver})
	wf := workflow.New(workflow.Deps{Store: st, Admission: svc, Clock: clock.Now, LeaseDuration: 15 * time.Minute})
	wid, tid := domain.WorkflowID(os.Getenv("AO_M3_CLAIM_WORKFLOW")), domain.WorkflowTaskID(os.Getenv("AO_M3_CLAIM_TASK"))
	task, found, err := st.GetWorkflowTask(ctx, wid, tid)
	if err != nil || !found {
		t.Fatalf("load task: %v %v", found, err)
	}
	fmt.Println("M3_READY")
	if _, err := io.ReadFull(os.Stdin, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	leader, leadErr := svc.MaintainLeadership(ctx)
	if leadErr == nil {
		if _, ok, err := st.CompleteSchedulerRecovery(ctx, leader.Fence, leader.Revision, now); err != nil || !ok {
			t.Fatalf("recover: %v %v", ok, err)
		}
	} else if !errors.Is(leadErr, domain.ErrSchedulerFence) {
		t.Fatal(leadErr)
	}
	claim, created, claimErr := wf.Claim(ctx, workflow.ClaimInput{WorkflowID: wid, TaskID: tid, ExpectedRevision: task.Revision, WorkerID: os.Getenv("AO_M3_CLAIM_OWNER"), IdempotencyKey: os.Getenv("AO_M3_CLAIM_OWNER")})
	result := m3ProcessResult{}
	if leadErr == nil {
		if claimErr != nil || !created {
			t.Fatalf("leader claim: %v %v", created, claimErr)
		}
		result.Won, result.AttemptID = true, claim.Attempt.ID
	} else if claimErr == nil || created {
		t.Fatal("non-leader claimed task")
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("M3_RESULT " + string(raw))
}
