package monitor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dsh2dsh/go-monitoringplugin/v2"

	"github.com/dsh2dsh/zrepl/config"
	"github.com/dsh2dsh/zrepl/daemon/filters"
	"github.com/dsh2dsh/zrepl/zfs"
)

func NewSnapCheck(resp *monitoringplugin.Response) *SnapCheck {
	return &SnapCheck{resp: resp}
}

type SnapCheck struct {
	counts bool
	oldest bool

	job    string
	prefix string
	warn   time.Duration
	crit   time.Duration

	resp *monitoringplugin.Response

	age       time.Duration
	snapCount uint
	snapName  string
	failed    bool

	datasets        map[string][]zfs.FilesystemVersion
	orderedDatasets []string
}

func (self *SnapCheck) WithPrefix(s string) *SnapCheck {
	self.prefix = s
	return self
}

func (self *SnapCheck) WithThresholds(warn, crit time.Duration) *SnapCheck {
	self.warn = warn
	self.crit = crit
	return self
}

func (self *SnapCheck) WithOldest(v bool) *SnapCheck {
	self.oldest = v
	return self
}

func (self *SnapCheck) WithResponse(resp *monitoringplugin.Response,
) *SnapCheck {
	self.resp = resp
	return self
}

func (self *SnapCheck) WithCounts(v bool) *SnapCheck {
	self.counts = v
	return self
}

func (self *SnapCheck) UpdateStatus(jobConfig *config.JobEnum) error {
	if err := self.Run(context.Background(), jobConfig); err != nil {
		return err
	}

	switch {
	case self.failed:
	case self.counts:
		self.updateStatus(monitoringplugin.OK,
			"all snapshots count: %d", self.snapCount)
	default:
		self.updateStatus(monitoringplugin.OK, "%s %q: %v",
			self.snapshotType(), self.snapName, self.age)
	}
	return nil
}

func (self *SnapCheck) Run(ctx context.Context, j *config.JobEnum) error {
	self.job = j.Name()
	datasets, err := self.jobDatasets(ctx, j)
	if err != nil {
		return err
	}

	if self.counts {
		return self.checkCounts(ctx, j, datasets)
	}
	return self.checkCreation(ctx, j, datasets)
}

func (self *SnapCheck) jobDatasets(
	ctx context.Context, jobConfig *config.JobEnum,
) (datasets []string, err error) {
	if self.orderedDatasets != nil {
		return self.orderedDatasets, nil
	}

	switch j := jobConfig.Ret.(type) {
	case *config.PushJob:
		datasets, err = self.datasetsFromFilter(ctx, j.Filesystems)
	case *config.SnapJob:
		datasets, err = self.datasetsFromFilter(ctx, j.Filesystems)
	case *config.SourceJob:
		datasets, err = self.datasetsFromFilter(ctx, j.Filesystems)
	case *config.PullJob:
		datasets, err = self.datasetsFromRootFs(ctx, j.RootFS, 0)
	case *config.SinkJob:
		datasets, err = self.datasetsFromRootFs(ctx, j.RootFS, 1)
	default:
		err = fmt.Errorf("unknown job type %T", j)
	}

	if err == nil {
		self.orderedDatasets = datasets
		self.datasets = make(map[string][]zfs.FilesystemVersion, len(datasets))
	}
	return
}

func (self *SnapCheck) datasetsFromFilter(
	ctx context.Context, ff config.FilesystemsFilter,
) ([]string, error) {
	filesystems, err := filters.DatasetMapFilterFromConfig(ff)
	if err != nil {
		return nil, fmt.Errorf("invalid filesystems: %w", err)
	}

	filtered := []string{}
	for item := range zfs.ZFSListIter(ctx, []string{"name"}, nil) {
		if item.Err != nil {
			return nil, item.Err
		} else if path, err := zfs.NewDatasetPath(item.Fields[0]); err != nil {
			return nil, err
		} else if ok, err := filesystems.Filter(path); err != nil {
			return nil, err
		} else if ok {
			filtered = append(filtered, item.Fields[0])
		}
	}
	return filtered, nil
}

func (self *SnapCheck) datasetsFromRootFs(
	ctx context.Context, rootFs string, skipN int,
) ([]string, error) {
	rootPath, err := zfs.NewDatasetPath(rootFs)
	if err != nil {
		return nil, err
	}

	filtered := []string{}
	for item := range zfs.ZFSListIter(ctx, []string{"name"}, nil, "-r", rootFs) {
		if item.Err != nil {
			return nil, item.Err
		}
		path, err := zfs.NewDatasetPath(item.Fields[0])
		if err != nil {
			return nil, err
		} else if path.Length() < rootPath.Length()+1+skipN {
			continue
		}
		if ph, err := zfs.ZFSGetFilesystemPlaceholderState(ctx, path); err != nil {
			return nil, err
		} else if ph.FSExists && !ph.IsPlaceholder {
			filtered = append(filtered, item.Fields[0])
		}
	}
	return filtered, nil
}

func (self *SnapCheck) checkCounts(ctx context.Context, j *config.JobEnum,
	datasets []string,
) error {
	rules := j.MonitorSnapshots().Count
	if len(rules) == 0 {
		return errors.New("no monitor rules defined")
	}

	for _, dataset := range datasets {
		if err := self.checkSnapsCounts(ctx, dataset, rules); err != nil {
			return err
		}
	}
	return nil
}

func (self *SnapCheck) checkSnapsCounts(ctx context.Context, fsName string,
	rules []config.MonitorCount,
) error {
	snapshots, err := self.snapshots(ctx, fsName)
	if err != nil {
		return err
	}

	prefixes := make([]string, len(rules))
	for i := range rules {
		prefixes[i] = rules[i].Prefix
	}
	grouped := groupSnapshots(snapshots, prefixes)

	for i := range rules {
		if !self.applyCountRule(&rules[i], fsName, &grouped[i]) {
			break
		}
	}
	return nil
}

func (self *SnapCheck) snapshots(ctx context.Context, fsName string,
) ([]zfs.FilesystemVersion, error) {
	if snaps, ok := self.datasets[fsName]; ok {
		return snaps, nil
	}

	fs, err := zfs.NewDatasetPath(fsName)
	if err != nil {
		return nil, err
	}

	snaps, err := zfs.ZFSListFilesystemVersions(ctx, fs,
		zfs.ListFilesystemVersionsOptions{Types: zfs.Snapshots})
	if err != nil {
		return nil, err
	}
	self.datasets[fsName] = snaps
	return snaps, err
}

func groupSnapshots(snapshots []zfs.FilesystemVersion, prefixes []string,
) []groupItem {
	grouped := make([]groupItem, len(prefixes))
	for i := range snapshots {
		s := &snapshots[i]
		for j, p := range prefixes {
			if p == "" || strings.HasPrefix(s.Name, p) {
				g := &grouped[j]
				g.Count++
				if g.Oldest == nil || s.Creation.Before(g.Oldest.Creation) {
					g.Oldest = s
				}
				if g.Latest == nil || s.Creation.After(g.Latest.Creation) {
					g.Latest = s
				}
				break
			}
		}
	}
	return grouped
}

type groupItem struct {
	Count  uint
	Oldest *zfs.FilesystemVersion
	Latest *zfs.FilesystemVersion
}

func (self *groupItem) Snapshot(oldest bool) *zfs.FilesystemVersion {
	if oldest {
		return self.Oldest
	}
	return self.Latest
}

func (self *SnapCheck) applyCountRule(rule *config.MonitorCount, fsName string,
	g *groupItem,
) bool {
	if g.Count == 0 && rule.Prefix == "" {
		return true
	} else if g.Count == 0 {
		self.resp.UpdateStatus(monitoringplugin.CRITICAL, fmt.Sprintf(
			"%q has no snapshots with prefix %q", fsName, rule.Prefix))
		return false
	}

	const msg = "%s: %q snapshots count: %d (%d)"
	switch {
	case g.Count >= rule.Critical:
		self.updateStatus(monitoringplugin.CRITICAL, msg,
			fsName, rule.Prefix, g.Count, rule.Critical)
		return false
	case rule.Warning > 0 && g.Count >= rule.Warning:
		self.updateStatus(monitoringplugin.WARNING, msg,
			fsName, rule.Prefix, g.Count, rule.Warning)
		return false
	default:
		self.snapCount += g.Count
	}
	return true
}

func (self *SnapCheck) checkCreation(ctx context.Context, j *config.JobEnum,
	datasets []string,
) error {
	rules, err := self.overrideRules(self.rulesByCreation(j))
	if err != nil {
		return err
	}

	for _, dataset := range datasets {
		if err := self.checkSnapsCreation(ctx, dataset, rules); err != nil {
			return err
		}
	}
	return nil
}

func (self *SnapCheck) overrideRules(rules []config.MonitorCreation,
) ([]config.MonitorCreation, error) {
	if self.prefix != "" {
		rules = []config.MonitorCreation{
			{
				Prefix:   self.prefix,
				Warning:  self.warn,
				Critical: self.crit,
			},
		}
	}

	if len(rules) == 0 {
		return nil, errors.New("no monitor rules or cli args defined")
	}
	return rules, nil
}

func (self *SnapCheck) rulesByCreation(j *config.JobEnum,
) []config.MonitorCreation {
	cfg := j.MonitorSnapshots()
	if self.oldest {
		return cfg.Oldest
	}
	return cfg.Latest
}

func (self *SnapCheck) checkSnapsCreation(
	ctx context.Context, fsName string, rules []config.MonitorCreation,
) error {
	snapshots, err := self.snapshots(ctx, fsName)
	if err != nil {
		return err
	}

	prefixes := make([]string, len(rules))
	for i := range rules {
		prefixes[i] = rules[i].Prefix
	}
	grouped := groupSnapshots(snapshots, prefixes)

	for i := range rules {
		s := grouped[i].Snapshot(self.oldest)
		if !self.applyCreationRule(&rules[i], s, fsName) {
			return nil
		}
	}
	return nil
}

func (self *SnapCheck) applyCreationRule(rule *config.MonitorCreation,
	snap *zfs.FilesystemVersion, fsName string,
) bool {
	if snap == nil && rule.Prefix == "" {
		return true
	} else if snap == nil {
		self.resp.UpdateStatus(monitoringplugin.CRITICAL, fmt.Sprintf(
			"%q has no snapshots with prefix %q", fsName, rule.Prefix))
		return false
	}

	const tooOldFmt = "%s %q too old: %q > %q"
	d := time.Since(snap.Creation).Truncate(time.Second)

	switch {
	case d >= rule.Critical:
		self.updateStatus(monitoringplugin.CRITICAL, tooOldFmt,
			self.snapshotType(), snap.FullPath(fsName), d, rule.Critical)
		return false
	case rule.Warning > 0 && d >= rule.Warning:
		self.updateStatus(monitoringplugin.WARNING, tooOldFmt,
			self.snapshotType(), snap.FullPath(fsName), d, rule.Warning)
		return false
	case self.age == 0:
		fallthrough
	case self.oldest && d > self.age:
		fallthrough
	case !self.oldest && d < self.age:
		self.age = d
		self.snapName = snap.Name
	}
	return true
}

func (self *SnapCheck) updateStatus(statusCode int, format string, a ...any) {
	self.failed = self.failed || statusCode != monitoringplugin.OK
	statusMessage := fmt.Sprintf("job %q: ", self.job) +
		fmt.Sprintf(format, a...)
	self.resp.UpdateStatus(statusCode, statusMessage)
}

func (self *SnapCheck) snapshotType() string {
	if self.oldest {
		return "oldest"
	}
	return "latest"
}

func (self *SnapCheck) Reset() *SnapCheck {
	self.age = 0
	self.snapCount = 0
	self.snapName = ""
	self.failed = false
	return self
}
