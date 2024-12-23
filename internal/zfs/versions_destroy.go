package zfs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"syscall"

	"github.com/dsh2dsh/zrepl/internal/util/envconst"
)

func ZFSDestroyFilesystemVersion(ctx context.Context, filesystem *DatasetPath, version *FilesystemVersion) (err error) {
	datasetPath := version.ToAbsPath(filesystem)

	// Sanity check...
	if !strings.ContainsAny(datasetPath, "@#") {
		return fmt.Errorf("sanity check failed: no @ or # character found in %q", datasetPath)
	}

	return ZFSDestroy(ctx, datasetPath)
}

var destroyerSingleton = destroyerImpl{}

type DestroySnapOp struct {
	Filesystem string
	Name       string
	ErrOut     *error
}

func (o *DestroySnapOp) String() string {
	return fmt.Sprintf("destroy operation %s@%s", o.Filesystem, o.Name)
}

func ZFSDestroyFilesystemVersions(ctx context.Context, reqs []*DestroySnapOp) {
	doDestroy(ctx, reqs, destroyerSingleton)
}

func setDestroySnapOpErr(b []*DestroySnapOp, err error) {
	for _, r := range b {
		*r.ErrOut = err
	}
}

type destroyer interface {
	Destroy(ctx context.Context, args []string) error
}

func doDestroy(ctx context.Context, reqs []*DestroySnapOp, e destroyer) {
	var validated []*DestroySnapOp
	for _, req := range reqs {
		// Filesystem and Snapshot should not be empty. ZFS will generally fail
		// because those are invalid destroy arguments, but we'd rather apply
		// defensive programming here (doing destroy after all).
		switch {
		case req.Filesystem == "":
			*req.ErrOut = errors.New("Filesystem must not be an empty string")
		case req.Name == "":
			*req.ErrOut = errors.New("Name must not be an empty string")
		default:
			validated = append(validated, req)
		}
	}
	reqs = validated
	doDestroyBatched(ctx, reqs, e)
}

func doDestroySeq(ctx context.Context, reqs []*DestroySnapOp, e destroyer) {
	for _, r := range reqs {
		*r.ErrOut = e.Destroy(ctx, []string{fmt.Sprintf("%s@%s", r.Filesystem, r.Name)})
	}
}

func doDestroyBatched(ctx context.Context, reqs []*DestroySnapOp, d destroyer) {
	perFS := buildBatches(reqs)
	for _, fsbatch := range perFS {
		doDestroyBatchedRec(ctx, fsbatch, d)
	}
}

func buildBatches(reqs []*DestroySnapOp) [][]*DestroySnapOp {
	if len(reqs) == 0 {
		return nil
	}
	sorted := make([]*DestroySnapOp, len(reqs))
	copy(sorted, reqs)
	sort.SliceStable(sorted, func(i, j int) bool {
		// by filesystem, then snap name
		fscmp := strings.Compare(sorted[i].Filesystem, sorted[j].Filesystem)
		if fscmp != 0 {
			return fscmp == -1
		}
		return strings.Compare(sorted[i].Name, sorted[j].Name) == -1
	})

	// group by fs
	var perFS [][]*DestroySnapOp
	consumed := 0
	maxBatchSize := envconst.Int("ZREPL_DESTROY_MAX_BATCH_SIZE", 0)
	for consumed < len(sorted) {
		batchConsumedUntil := consumed
		for ; batchConsumedUntil < len(sorted) && (maxBatchSize < 1 || batchConsumedUntil-consumed < maxBatchSize) && sorted[batchConsumedUntil].Filesystem == sorted[consumed].Filesystem; batchConsumedUntil++ {
		}
		perFS = append(perFS, sorted[consumed:batchConsumedUntil])
		consumed = batchConsumedUntil
	}
	return perFS
}

// batch must be on same Filesystem, panics otherwise
func tryBatch(ctx context.Context, batch []*DestroySnapOp, d destroyer) error {
	if len(batch) == 0 {
		return nil
	}

	batchFS := batch[0].Filesystem
	batchNames := make([]string, len(batch))
	for i := range batchNames {
		batchNames[i] = batch[i].Name
		if batchFS != batch[i].Filesystem {
			panic("inconsistent batch")
		}
	}
	batchArg := fmt.Sprintf("%s@%s", batchFS, strings.Join(batchNames, ","))
	return d.Destroy(ctx, []string{batchArg})
}

// fsbatch must be on same filesystem
func doDestroyBatchedRec(ctx context.Context, fsbatch []*DestroySnapOp, d destroyer) {
	if len(fsbatch) <= 1 {
		doDestroySeq(ctx, fsbatch, d)
		return
	}

	err := tryBatch(ctx, fsbatch, d)
	if err == nil {
		setDestroySnapOpErr(fsbatch, nil)
		return
	} else {
		var pe *os.PathError
		if errors.As(err, &pe) && errors.Is(pe.Err, syscall.E2BIG) {
			// see TestExcessiveArgumentsResultInE2BIG
			// try halving batch size, assuming snapshots names are roughly the same length
			debug("batch destroy: E2BIG encountered: %s", err)
			doDestroyBatchedRec(ctx, fsbatch[0:len(fsbatch)/2], d)
			doDestroyBatchedRec(ctx, fsbatch[len(fsbatch)/2:], d)
			return
		}
	}

	singleRun := fsbatch // the destroys that will be tried sequentially after "smart" error handling below

	var errDestroy *DestroySnapshotsError
	if errors.As(err, &errDestroy) {
		// eliminate undestroyable datasets from batch and try it once again
		strippedBatch, remaining := make([]*DestroySnapOp, 0, len(fsbatch)), make([]*DestroySnapOp, 0, len(fsbatch))

		for _, b := range fsbatch {
			isUndestroyable := false
			for _, undestroyable := range errDestroy.Undestroyable {
				if undestroyable == b.Name {
					isUndestroyable = true
					break
				}
			}
			if isUndestroyable {
				remaining = append(remaining, b)
			} else {
				strippedBatch = append(strippedBatch, b)
			}
		}

		err := tryBatch(ctx, strippedBatch, d)
		if err != nil {
			// run entire batch sequentially if the stripped one fails
			// (it shouldn't because we stripped erroneous datasets)
			singleRun = fsbatch // shadow
		} else {
			setDestroySnapOpErr(strippedBatch, nil) // these ones worked
			singleRun = remaining                   // shadow
		}
		// fallthrough
	}

	doDestroySeq(ctx, singleRun, d)
}

type destroyerImpl struct{}

func (d destroyerImpl) Destroy(ctx context.Context, args []string) error {
	if len(args) != 1 {
		// we have no use case for this at the moment, so let's crash (safer than destroying something unexpectedly)
		panic(fmt.Sprintf("unexpected number of arguments: %v", args))
	}
	// we know that we are only using this for snapshots, so also sanity check for an @ in args[0]
	if !strings.ContainsAny(args[0], "@") {
		panic(fmt.Sprintf("sanity check: expecting '@' in call to Destroy, got %q", args[0]))
	}
	return ZFSDestroy(ctx, args[0])
}
