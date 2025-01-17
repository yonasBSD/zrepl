package snapper

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dsh2dsh/zrepl/internal/daemon/hooks"
	"github.com/dsh2dsh/zrepl/internal/daemon/logging"
	"github.com/dsh2dsh/zrepl/internal/logger"
	"github.com/dsh2dsh/zrepl/internal/util/chainlock"
	"github.com/dsh2dsh/zrepl/internal/zfs"
)

type planArgs struct {
	prefix          string
	timestampFormat string
	timestampLocal  bool
	hooks           hooks.List
}

type plan struct {
	mtx   chainlock.L
	args  planArgs
	snaps map[*zfs.DatasetPath]*snapProgress
}

func makePlan(args planArgs, fss []*zfs.DatasetPath) *plan {
	snaps := make(map[*zfs.DatasetPath]*snapProgress, len(fss))
	for _, fs := range fss {
		snaps[fs] = &snapProgress{state: SnapPending}
	}
	return &plan{snaps: snaps, args: args}
}

type SnapState uint

const (
	SnapPending SnapState = 1 << iota
	SnapStarted
	SnapDone
	SnapError
)

func (self SnapState) String() string {
	switch self {
	case SnapPending:
		return "SnapPending"
	case SnapStarted:
		return "SnapStarted"
	case SnapDone:
		return "SnapDone"
	case SnapError:
		return "SnapError"
	}
	return "SnapState(" + strconv.FormatInt(int64(self), 10) + ")"
}

// All fields protected by Snapper.mtx
type snapProgress struct {
	state SnapState

	// SnapStarted, SnapDone, SnapError
	name     string
	startAt  time.Time
	hookPlan *hooks.Plan

	// SnapDone
	doneAt time.Time

	// SnapErr TODO disambiguate state
	runResults hooks.PlanReport
}

func (plan *plan) formatNow(format string, localTime bool) string {
	now := time.Now()
	if !localTime {
		now = now.UTC()
	}
	switch strings.ToLower(format) {
	case "dense":
		format = "20060102_150405_MST"
	case "human":
		format = "2006-01-02_15:04:05"
	case "iso-8601":
		format = "2006-01-02T15:04:05.000Z"
	case "unix-seconds":
		return strconv.FormatInt(now.Unix(), 10)
	}
	return now.Format(format)
}

func (plan *plan) execute(ctx context.Context, dryRun bool) (ok bool) {
	hookMatchCount := make(map[hooks.Hook]int, len(plan.args.hooks))
	for _, h := range plan.args.hooks {
		hookMatchCount[h] = 0
	}

	anyFsHadErr := false
	// TODO channel programs -> allow a little jitter?
	for fs, progress := range plan.snaps {
		suffix := plan.formatNow(plan.args.timestampFormat,
			plan.args.timestampLocal)
		snapname := fmt.Sprintf("%s%s", plan.args.prefix, suffix)

		ctx := logging.With(ctx, slog.String("fs", fs.ToString()),
			slog.String("snap", snapname))

		hookEnvExtra := map[string]string{
			hooks.EnvFS:       fs.ToString(),
			hooks.EnvSnapshot: snapname,
		}

		jobCallback := hooks.NewCallbackHookForFilesystem("snapshot", fs, func(ctx context.Context) (err error) {
			l := getLogger(ctx)
			l.Debug("create snapshot")
			err = zfs.ZFSSnapshot(ctx, fs, snapname, false) // TODO propagate context to ZFSSnapshot
			if err != nil {
				logger.WithError(l, err, "cannot create snapshot")
			}
			return
		})

		fsHadErr := false
		var hookPlanReport hooks.PlanReport
		var hookPlan *hooks.Plan
		{
			filteredHooks, err := plan.args.hooks.CopyFilteredForFilesystem(fs)
			if err != nil {
				logger.WithError(getLogger(ctx), err, "unexpected filter error")
				fsHadErr = true
				goto updateFSState
			}
			// account for running hooks
			for _, h := range filteredHooks {
				hookMatchCount[h]++
			}

			var planErr error
			hookPlan, planErr = hooks.NewPlan(filteredHooks, hooks.PhaseSnapshot, jobCallback, hookEnvExtra)
			if planErr != nil {
				fsHadErr = true
				logger.WithError(getLogger(ctx), planErr, "cannot create job hook plan")
				goto updateFSState
			}
		}

		plan.mtx.HoldWhile(func() {
			progress.name = snapname
			progress.startAt = time.Now()
			progress.hookPlan = hookPlan
			progress.state = SnapStarted
		})

		{
			getLogger(ctx).
				With(slog.String("report", hookPlan.Report().String())).
				Debug("begin run job plan")
			hookPlan.Run(ctx, dryRun)
			hookPlanReport = hookPlan.Report()
			fsHadErr = hookPlanReport.HadError() // not just fatal errors
			if fsHadErr {
				getLogger(ctx).
					With(slog.String("report", hookPlanReport.String())).
					Error("end run job plan with error")
			} else {
				getLogger(ctx).
					With(slog.String("report", hookPlanReport.String())).
					Info("end run job plan successful")
			}
		}

	updateFSState:
		anyFsHadErr = anyFsHadErr || fsHadErr
		plan.mtx.HoldWhile(func() {
			progress.doneAt = time.Now()
			progress.state = SnapDone
			if fsHadErr {
				progress.state = SnapError
			}
			progress.runResults = hookPlanReport
		})
	}

	for h, mc := range hookMatchCount {
		if mc == 0 {
			hookIdx := -1
			for idx, ah := range plan.args.hooks {
				if ah == h {
					hookIdx = idx
					break
				}
			}
			getLogger(ctx).
				With(slog.String("hook", h.String()),
					slog.Int("hook_number", hookIdx+1)).
				Warn("hook did not match any snapshotted filesystems")
		}
	}

	return !anyFsHadErr
}

type ReportFilesystem struct {
	Path  string
	State SnapState

	// Valid in SnapStarted and later
	SnapName      string
	StartAt       time.Time
	Hooks         string
	HooksHadError bool

	// Valid in SnapDone | SnapError
	DoneAt time.Time
}

func (plan *plan) report() []*ReportFilesystem {
	plan.mtx.Lock()
	defer plan.mtx.Unlock()

	pReps := make([]*ReportFilesystem, 0, len(plan.snaps))
	for fs, p := range plan.snaps {
		var hooksStr string
		var hooksHadError bool
		if p.hookPlan != nil {
			hooksStr, hooksHadError = p.report()
		}
		pReps = append(pReps, &ReportFilesystem{
			Path:          fs.ToString(),
			State:         p.state,
			SnapName:      p.name,
			StartAt:       p.startAt,
			DoneAt:        p.doneAt,
			Hooks:         hooksStr,
			HooksHadError: hooksHadError,
		})
	}

	sort.Slice(pReps, func(i, j int) bool {
		return strings.Compare(pReps[i].Path, pReps[j].Path) == -1
	})

	return pReps
}

func (p *snapProgress) report() (hooksStr string, hooksHadError bool) {
	hr := p.hookPlan.Report()
	// FIXME: technically this belongs into client
	// but we can't serialize hooks.Step ATM
	rightPad := func(str string, length int, pad string) string {
		if len(str) > length {
			return str[:length]
		}
		return str + strings.Repeat(pad, length-len(str))
	}
	hooksHadError = hr.HadError()
	rows := make([][]string, len(hr))
	const numCols = 4
	lens := make([]int, numCols)
	for i, e := range hr {
		rows[i] = make([]string, numCols)
		rows[i][0] = strconv.Itoa(i + 1)
		rows[i][1] = e.Status.String()
		runTime := "..."
		if e.Status != hooks.StepPending {
			runTime = e.End.Sub(e.Begin).Round(time.Millisecond).String()
		}
		rows[i][2] = runTime
		rows[i][3] = ""
		if e.Report != nil {
			rows[i][3] = e.Report.String()
		}
		for j, col := range lens {
			if len(rows[i][j]) > col {
				lens[j] = len(rows[i][j])
			}
		}
	}
	rowsFlat := make([]string, len(hr))
	for i, r := range rows {
		colsPadded := make([]string, len(r))
		for j, c := range r[:len(r)-1] {
			colsPadded[j] = rightPad(c, lens[j], " ")
		}
		colsPadded[len(r)-1] = r[len(r)-1]
		rowsFlat[i] = strings.Join(colsPadded, " ")
	}
	hooksStr = strings.Join(rowsFlat, "\n")

	return hooksStr, hooksHadError
}
