package endpoint

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/dsh2dsh/zrepl/internal/zfs"
)

// returns the short name (no fs# prefix)
func makeJobAndGuidBookmarkName(prefix string, fs string, guid uint64, jobid string) (string, error) {
	bmname := fmt.Sprintf(prefix+"_G_%016x_J_%s", guid, jobid)
	if err := zfs.EntityNamecheck(fmt.Sprintf("%s#%s", fs, bmname), zfs.EntityTypeBookmark); err != nil {
		return "", err
	}
	return bmname, nil
}

var jobAndGuidBookmarkRE = regexp.MustCompile(`(.+)_G_([0-9a-f]{16})_J_(.+)$`)

func parseJobAndGuidBookmarkName(fullname string, prefix string) (guid uint64, jobID JobID, _ error) {
	if len(prefix) == 0 {
		panic("prefix must not be empty")
	}

	if err := zfs.EntityNamecheck(fullname, zfs.EntityTypeBookmark); err != nil {
		return 0, JobID{}, err
	}

	_, _, name, err := zfs.DecomposeVersionString(fullname)
	if err != nil {
		return 0, JobID{}, fmt.Errorf("decompose bookmark name: %w", err)
	}

	match := jobAndGuidBookmarkRE.FindStringSubmatch(name)
	if match == nil {
		return 0, JobID{}, fmt.Errorf("bookmark name does not match regex %q", jobAndGuidBookmarkRE.String())
	}
	if match[1] != prefix {
		return 0, JobID{}, fmt.Errorf("prefix component does not match: expected %q, got %q", prefix, match[1])
	}

	guid, err = strconv.ParseUint(match[2], 16, 64)
	if err != nil {
		return 0, JobID{}, fmt.Errorf("parse guid component: %q: %w", match[2], err)
	}

	jobID, err = MakeJobID(match[3])
	if err != nil {
		return 0, JobID{}, fmt.Errorf("parse jobid component: %q: %w", match[3], err)
	}

	return guid, jobID, nil
}
