package pruning

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/dsh2dsh/zrepl/internal/config"
	"github.com/dsh2dsh/zrepl/internal/daemon/logging"
	"github.com/dsh2dsh/zrepl/internal/pruning/retentiongrid"
)

// KeepGrid fits snapshots that match a given regex into a retentiongrid.Grid,
// uses the most recent snapshot among those that match the regex as 'now',
// and deletes all snapshots that do not fit the grid specification.
type KeepGrid struct {
	retentionGrid *retentiongrid.Grid
	re            *regexp.Regexp
}

var _ KeepRule = (*KeepGrid)(nil)

func NewKeepGrid(in *config.PruneGrid) (p *KeepGrid, err error) {
	if in.Regex == "" {
		return nil, errors.New("Regex must not be empty")
	}
	re, err := regexp.Compile(in.Regex)
	if err != nil {
		return nil, fmt.Errorf("Regex is invalid: %w", err)
	}

	return newKeepGrid(re, in.Grid)
}

func MustNewKeepGrid(regex, gridspec string) *KeepGrid {
	ris, err := config.ParseRetentionIntervalSpec(gridspec)
	if err != nil {
		panic(err)
	}

	re := regexp.MustCompile(regex)

	grid, err := newKeepGrid(re, ris)
	if err != nil {
		panic(err)
	}
	return grid
}

func newKeepGrid(re *regexp.Regexp, configIntervals []config.RetentionInterval) (*KeepGrid, error) {
	if re == nil {
		panic("re must not be nil")
	}

	if len(configIntervals) == 0 {
		return nil, errors.New("retention grid must specify at least one interval")
	}

	intervals := make([]retentiongrid.Interval, len(configIntervals))
	for i := range configIntervals {
		intervals[i] = &configIntervals[i]
	}

	// Assert intervals are of increasing length (not necessarily required, but indicates config mistake)
	lastDuration := time.Duration(0)
	for i := range intervals {

		if intervals[i].Length() < lastDuration {
			// If all intervals before were keep=all, this is ok
			allPrevKeepCountAll := true
			for j := i - 1; allPrevKeepCountAll && j >= 0; j-- {
				allPrevKeepCountAll = intervals[j].KeepCount() == config.RetentionGridKeepCountAll
			}
			if allPrevKeepCountAll {
				goto isMonotonicIncrease
			}
			return nil, errors.New("retention grid interval length must be monotonically increasing")
		}
	isMonotonicIncrease:
		lastDuration = intervals[i].Length()
	}

	return &KeepGrid{
		retentionGrid: retentiongrid.NewGrid(intervals),
		re:            re,
	}, nil
}

var gridDeprecated sync.Once

// Prune filters snapshots with the retention grid.
func (p *KeepGrid) KeepRule(ctx context.Context, snaps []Snapshot,
) (destroyList []Snapshot) {
	gridDeprecated.Do(func() {
		log := logging.GetLogger(ctx, logging.SubsysPruning)
		log.Warn("'grid' pruning depricated. Consider using of multiple 'snap' jobs with different 'prefix' + 'last_n' pruning.")
	})

	matching, notMatching := partitionSnapList(snaps,
		func(snapshot Snapshot) bool {
			return p.re.MatchString(snapshot.Name())
		})

	// snaps that don't match the regex are not kept by this rule
	destroyList = append(destroyList, notMatching...)

	if len(matching) == 0 {
		return destroyList
	}

	// Evaluate retention grid
	entrySlice := make([]retentiongrid.Entry, 0)
	for i := range matching {
		entrySlice = append(entrySlice, matching[i])
	}
	_, gridDestroyList := p.retentionGrid.FitEntries(entrySlice)

	// Revert adaptors
	for i := range gridDestroyList {
		destroyList = append(destroyList, gridDestroyList[i].(Snapshot))
	}
	return destroyList
}
