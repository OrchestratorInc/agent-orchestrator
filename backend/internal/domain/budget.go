package domain

import (
	"errors"
	"strings"
	"time"
)

// ErrBudgetPolicy 表示预算策略字段或版本不能用于准入。
var (
	ErrBudgetPolicy   = errors.New("invalid or stale budget policy")
	ErrBudgetUnknown  = errors.New("budget cost or reservation is unknown")
	ErrBudgetExceeded = errors.New("budget cannot admit the reservation")
	ErrBudgetOverflow = errors.New("budget arithmetic overflow")
)

// BudgetObserve 只记录预算；同组常量定义强制级别、窗口和预留来源。
const (
	BudgetObserve               = "observe"
	BudgetSoft                  = "soft"
	BudgetHardAdmission         = "hard_admission"
	BudgetLifetime              = "lifetime"
	BudgetMonthlyUTC            = "monthly_utc"
	ReservationOperatorApproved = "operator_approved"
	ReservationTrustedBound     = "trusted_bound"
)

// BudgetPolicy 使用 nano-USD；明确批准金额和依据与策略 revision 一同持久化。
type BudgetPolicy struct {
	ID, Scope, ScopeID, Currency, Mode, Period string
	LimitNanos                                 int64
	ReservationNanos                           *int64
	ReservationBasis, ReservationRef           string
	Revision                                   int64
}

// RevisionKey 隔离预算与资源策略同名 ID，避免 revision 证据互相覆盖。
func (p BudgetPolicy) RevisionKey() string { return "budget:" + p.ID }

// Validate 保证三级作用域、货币、窗口和预留依据可解释。
func (p BudgetPolicy) Validate() error {
	if strings.TrimSpace(p.ID) == "" || p.Currency != "USD" || p.LimitNanos < 0 || p.Revision < 0 {
		return ErrBudgetPolicy
	}
	if p.Mode != BudgetObserve && p.Mode != BudgetSoft && p.Mode != BudgetHardAdmission {
		return ErrBudgetPolicy
	}
	if p.Scope != "global" && p.Scope != "workflow" && p.Scope != "task" {
		return ErrBudgetPolicy
	}
	if (p.Scope == "global") != (p.ScopeID == "") {
		return ErrBudgetPolicy
	}
	if p.Period != BudgetLifetime && (p.Scope != "global" || p.Period != BudgetMonthlyUTC) {
		return ErrBudgetPolicy
	}
	if p.ReservationNanos != nil && (*p.ReservationNanos < 0 || !ValidReservationBasis(p.ReservationBasis, p.ReservationRef)) {
		return ErrBudgetPolicy
	}
	if p.ReservationNanos == nil && (p.ReservationBasis != "" || p.ReservationRef != "") {
		return ErrBudgetPolicy
	}
	return nil
}

// ValidReservationBasis 不接受均价或无来源估算作为预算上界。
func ValidReservationBasis(basis, ref string) bool {
	return (basis == ReservationOperatorApproved || basis == ReservationTrustedBound) && strings.TrimSpace(ref) != ""
}

// Window 返回 UTC 月窗或永久窗口，历史预留不会随月份变更归属。
func (p BudgetPolicy) Window(now time.Time) string {
	if p.Period == BudgetMonthlyUTC {
		return now.UTC().Format("2006-01")
	}
	return BudgetLifetime
}

// BudgetReservation 对每个 admission、策略与窗口保留一次预留及明确金额质量。
type BudgetReservation struct {
	AdmissionID             string
	AttemptID               WorkflowAttemptID
	PolicyID, Window        string
	AmountNanos             *int64
	Basis, Reference, State string
	Revision                int64
}
