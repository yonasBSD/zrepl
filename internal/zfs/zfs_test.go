package zfs

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	zfsprop "github.com/dsh2dsh/zrepl/internal/zfs/property"
	"github.com/dsh2dsh/zrepl/internal/zfs/zfscmd"
)

// FIXME make this a platformtest
func TestZFSListHandlesProducesZFSErrorOnNonZeroExit(t *testing.T) {
	t.SkipNow() // FIXME ZFS_BINARY does not work if tests run in parallel

	var err error

	ZfsBin = "./test_helpers/zfs_failer.sh"

	_, err = ZFSList(t.Context(), []string{"fictionalprop"}, "nonexistent/dataset")

	require.Error(t, err)
	var zfsError *ZFSError
	require.ErrorAs(t, err, &zfsError)
	assert.Equal(t, "error: this is a mock\n", string(zfsError.Stderr))
}

func TestZFSPropertySource(t *testing.T) {
	tcs := []struct {
		in  PropertySource
		exp []string
	}{
		{
			in: SourceAny,
			// although empty prefix matches any source
			exp: []string{"local", "default", "inherited", "-", "temporary", "received", ""},
		},
		{
			in:  SourceTemporary,
			exp: []string{"temporary"},
		},
		{
			in:  SourceLocal | SourceInherited,
			exp: []string{"local", "inherited"},
		},
	}

	toSet := func(in []string) map[string]struct{} {
		m := make(map[string]struct{}, len(in))
		for _, s := range in {
			m[s] = struct{}{}
		}
		return m
	}

	for _, tc := range tcs {

		t.Logf("TEST CASE %v", tc)

		// give the parsing code some coverage
		for _, e := range tc.exp {
			if e == "" {
				continue // "" is the prefix that matches SourceAny
			}
			s, err := parsePropertySource(e)
			require.NoError(t, err)
			t.Logf("s: %x %s", s, s)
			t.Logf("in: %x %s", tc.in, tc.in)
			assert.NotEqual(t, 0, s&tc.in)
		}

		// prefix matching
		res := tc.in.zfsGetSourceFieldPrefixes()
		resSet := toSet(res)
		expSet := toSet(tc.exp)
		assert.Equal(t, expSet, resSet)
	}
}

func TestDrySendInfo(t *testing.T) {
	// # full send
	// $ zfs send -Pnv -t 1-9baebea70-b8-789c636064000310a500c4ec50360710e72765a52697303030419460caa7a515a79680647ce0f26c48f2499525a9c5405ac3c90fabfe92fcf4d2cc140686b30972c7850efd0cd24092e704cbe725e6a632305415e5e797e803cd2ad14f743084b805001b201795
	fullSend := `
resume token contents:
nvlist version: 0
	object = 0x2
	offset = 0x4c0000
	bytes = 0x4e4228
	toguid = 0x52f9c212c71e60cd
	toname = zroot/test/a@1
full	zroot/test/a@1	5389768
`

	// # incremental send with token
	// $ zfs send -nvP -t 1-ef01e717e-e0-789c636064000310a501c49c50360710a715e5e7a69766a63040c1d904b9e342877e062900d9ec48eaf293b252934b181898a0ea30e4d3d28a534b40323e70793624f9a4ca92d46220fdc1ce0fabfe927c882bc46c8a0a9f71ad3baf8124cf0996cf4bcc4d6560a82acacf2fd1079a55a29fe86004710b00d8ae1f93
	incSend := `
resume token contents:
nvlist version: 0
	fromguid = 0x52f9c212c71e60cd
	object = 0x2
	offset = 0x4c0000
	bytes = 0x4e3ef0
	toguid = 0xcfae0ae671723c16
	toname = zroot/test/a@2
incremental	zroot/test/a@1	zroot/test/a@2	5383936
`

	// # incremental send with token + bookmark
	// $ zfs send -nvP -t 1-ef01e717e-e0-789c636064000310a501c49c50360710a715e5e7a69766a63040c1d904b9e342877e062900d9ec48eaf293b252934b181898a0ea30e4d3d28a534b40323e70793624f9a4ca92d46220fdc1ce0fabfe927c882bc46c8a0a9f71ad3baf8124cf0996cf4bcc4d6560a82acacf2fd1079a55a29fe86004710b00d8ae1f93
	incSendBookmark := `
resume token contents:
nvlist version: 0
	fromguid = 0x52f9c212c71e60cd
	object = 0x2
	offset = 0x4c0000
	bytes = 0x4e3ef0
	toguid = 0xcfae0ae671723c16
	toname = zroot/test/a@2
incremental	zroot/test/a#1	zroot/test/a@2	5383312

`

	// incremental send without token
	// $ sudo zfs send -nvP -i @1 zroot/test/a@2
	incNoToken := `
incremental	1	zroot/test/a@2	10511856
size	10511856
`
	// full send without token
	// $ sudo zfs send -nvP  zroot/test/a@3
	fullNoToken := `
full	zroot/test/a@3	10518512
size	10518512
`

	// zero-length incremental send on ZoL 0.7.12
	// (it omits the size field as well as the size line if size is 0)
	// see https://github.com/zrepl/zrepl/issues/289
	// fixed in https://github.com/openzfs/zfs/commit/835db58592d7d947e5818eb7281882e2a46073e0#diff-66bd524398bcd2ac70d90925ab6d8073L1245
	incZeroSized_0_7_12 := `
incremental	p1 with/ spaces d1@1 with space	p1 with/ spaces d1@2 with space
`

	fullZeroSized_0_7_12 := `
full	p1 with/ spaces d1@2 with space
`

	fullWithSpaces := "\nfull\tpool1/otherjob/ds with spaces@blaffoo\t12912\nsize\t12912\n"
	fullWithSpacesInIntermediateComponent := "\nfull\tpool1/otherjob/another ds with spaces/childfs@blaffoo\t12912\nsize\t12912\n"
	incrementalWithSpaces := "\nincremental\tblaffoo\tpool1/otherjob/another ds with spaces@blaffoo2\t624\nsize\t624\n"
	incrementalWithSpacesInIntermediateComponent := "\nincremental\tblaffoo\tpool1/otherjob/another ds with spaces/childfs@blaffoo2\t624\nsize\t624\n"

	type tc struct {
		name   string
		in     string
		exp    *DrySendInfo
		expErr bool
	}

	tcs := []tc{
		{
			name: "fullSend",
			in:   fullSend,
			exp: &DrySendInfo{
				Type:         DrySendTypeFull,
				Filesystem:   "zroot/test/a",
				From:         "",
				To:           "zroot/test/a@1",
				SizeEstimate: 5389768,
			},
		},
		{
			name: "incSend",
			in:   incSend,
			exp: &DrySendInfo{
				Type:         DrySendTypeIncremental,
				Filesystem:   "zroot/test/a",
				From:         "zroot/test/a@1",
				To:           "zroot/test/a@2",
				SizeEstimate: 5383936,
			},
		},
		{
			name: "incSendBookmark",
			in:   incSendBookmark,
			exp: &DrySendInfo{
				Type:         DrySendTypeIncremental,
				Filesystem:   "zroot/test/a",
				From:         "zroot/test/a#1",
				To:           "zroot/test/a@2",
				SizeEstimate: 5383312,
			},
		},
		{
			name: "incNoToken",
			in:   incNoToken,
			exp: &DrySendInfo{
				Type:       DrySendTypeIncremental,
				Filesystem: "zroot/test/a",
				// as can be seen in the string incNoToken,
				// we cannot infer whether the incremental source is a snapshot or bookmark
				From:         "1", // yes, this is actually correct on ZoL 0.7.11
				To:           "zroot/test/a@2",
				SizeEstimate: 10511856,
			},
		},
		{
			name: "fullNoToken",
			in:   fullNoToken,
			exp: &DrySendInfo{
				Type:         DrySendTypeFull,
				Filesystem:   "zroot/test/a",
				From:         "",
				To:           "zroot/test/a@3",
				SizeEstimate: 10518512,
			},
		},
		{
			name: "fullWithSpaces",
			in:   fullWithSpaces,
			exp: &DrySendInfo{
				Type:         DrySendTypeFull,
				Filesystem:   "pool1/otherjob/ds with spaces",
				From:         "",
				To:           "pool1/otherjob/ds with spaces@blaffoo",
				SizeEstimate: 12912,
			},
		},
		{
			name: "fullWithSpacesInIntermediateComponent",
			in:   fullWithSpacesInIntermediateComponent,
			exp: &DrySendInfo{
				Type:         DrySendTypeFull,
				Filesystem:   "pool1/otherjob/another ds with spaces/childfs",
				From:         "",
				To:           "pool1/otherjob/another ds with spaces/childfs@blaffoo",
				SizeEstimate: 12912,
			},
		},
		{
			name: "incrementalWithSpaces",
			in:   incrementalWithSpaces,
			exp: &DrySendInfo{
				Type:         DrySendTypeIncremental,
				Filesystem:   "pool1/otherjob/another ds with spaces",
				From:         "blaffoo",
				To:           "pool1/otherjob/another ds with spaces@blaffoo2",
				SizeEstimate: 624,
			},
		},
		{
			name: "incrementalWithSpacesInIntermediateComponent",
			in:   incrementalWithSpacesInIntermediateComponent,
			exp: &DrySendInfo{
				Type:         DrySendTypeIncremental,
				Filesystem:   "pool1/otherjob/another ds with spaces/childfs",
				From:         "blaffoo",
				To:           "pool1/otherjob/another ds with spaces/childfs@blaffoo2",
				SizeEstimate: 624,
			},
		},
		{
			name: "incrementalZeroSizedOpenZFS_pre0.7.12",
			in:   incZeroSized_0_7_12,
			exp: &DrySendInfo{
				Type:         DrySendTypeIncremental,
				Filesystem:   "p1 with/ spaces d1",
				From:         "p1 with/ spaces d1@1 with space",
				To:           "p1 with/ spaces d1@2 with space",
				SizeEstimate: 0,
			},
		},
		{
			name: "fullZeroSizedOpenZFS_pre0.7.12",
			in:   fullZeroSized_0_7_12,
			exp: &DrySendInfo{
				Type:         DrySendTypeFull,
				Filesystem:   "p1 with/ spaces d1",
				To:           "p1 with/ spaces d1@2 with space",
				SizeEstimate: 0,
			},
		},
		{
			name: "incremental package without size",
			in: `
incremental	snap1	pool1/ds@snap2	624
incremental	snap2	pool1/ds@snap3	624
`,
			exp: &DrySendInfo{
				Type:         DrySendTypeIncremental,
				Filesystem:   "pool1/ds",
				From:         "snap1",
				To:           "pool1/ds@snap3",
				SizeEstimate: 1248,
			},
		},
		{
			name: "incremental package with size",
			in: `
incremental	snap1	pool1/ds@snap2	624
incremental	snap2	pool1/ds@snap3	624
size	1248
`,
			exp: &DrySendInfo{
				Type:         DrySendTypeIncremental,
				Filesystem:   "pool1/ds",
				From:         "snap1",
				To:           "pool1/ds@snap3",
				SizeEstimate: 1248,
			},
		},
		{
			name: "dry send type changed",
			in: `
full	pool1/ds@snap2	624
incremental	snap2	pool1/ds@snap3	624
size	1248
`,
			expErr: true,
		},
		{
			name: "no match for info line",
			in: `
size	1248
`,
			expErr: true,
		},
		{
			name: "cannot not parse size",
			in: `
incremental	snap2	pool1/ds@snap3	624
size	XXX
`,
			expErr: true,
		},
		{
			name: "incremental package with size less",
			in: `
incremental	snap1	pool1/ds@snap2	624
incremental	snap2	pool1/ds@snap3	624
size	1000
`,
			exp: &DrySendInfo{
				Type:         DrySendTypeIncremental,
				Filesystem:   "pool1/ds",
				From:         "snap1",
				To:           "pool1/ds@snap3",
				SizeEstimate: 1248,
			},
		},
		{
			name: "incremental package with size more",
			in: `
incremental	snap1	pool1/ds@snap2	624
incremental	snap2	pool1/ds@snap3	624
size	1500
`,
			exp: &DrySendInfo{
				Type:         DrySendTypeIncremental,
				Filesystem:   "pool1/ds",
				From:         "snap1",
				To:           "pool1/ds@snap3",
				SizeEstimate: 1500,
			},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in[1:] // strip first newline
			var si DrySendInfo
			err := si.unmarshalZFSOutput([]byte(in))
			t.Logf("%#v", &si)
			t.Logf("err=%T %s", err, err)

			if tc.expErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.exp, &si)
			}
		})
	}
}

func TestTryRecvDestroyOrOverwriteEncryptedErr(t *testing.T) {
	msg := "cannot receive new filesystem stream: zfs receive -F cannot be used to destroy an encrypted filesystem or overwrite an unencrypted one with an encrypted one\n"

	err := tryRecvDestroyOrOverwriteEncryptedErr([]byte(msg))
	require.NotNil(t, err)
	assert.EqualError(t, err, strings.TrimSpace(msg))
}

func TestZFSSendArgsBuildSendFlags(t *testing.T) {
	type args = ZFSSendFlags
	type SendTest struct {
		conf         args
		exactMatch   bool
		flagsInclude []string
		flagsExclude []string
	}

	sendTests := map[string]SendTest{
		"Empty Args": {
			conf:         args{},
			flagsInclude: []string{},
			flagsExclude: []string{"-w", "-p", "-b"},
		},
		"Raw": {
			conf:         args{Raw: true},
			flagsInclude: []string{"-w"},
			flagsExclude: []string{},
		},
		"Encrypted": {
			conf:         args{Encrypted: true},
			flagsInclude: []string{"-w"},
			flagsExclude: []string{},
		},
		"Unencrypted_And_Raw": {
			conf:         args{Raw: true},
			flagsInclude: []string{"-w"},
			flagsExclude: []string{},
		},
		"Encrypted_And_Raw": {
			conf:         args{Encrypted: true, Raw: true},
			flagsInclude: []string{"-w"},
			flagsExclude: []string{},
		},
		"Send properties": {
			conf:         args{Properties: true},
			flagsInclude: []string{"-p"},
			flagsExclude: []string{},
		},
		"Send backup properties": {
			conf:         args{BackupProperties: true},
			flagsInclude: []string{"-b"},
			flagsExclude: []string{},
		},
		"Send -b and -p": {
			conf:         args{Properties: true, BackupProperties: true},
			flagsInclude: []string{"-p", "-b"},
			flagsExclude: []string{},
		},
		"Send resume state": {
			conf:         args{Saved: true},
			flagsInclude: []string{"-S"},
		},
		"Resume token wins if not empty": {
			conf:         args{ResumeToken: "$theresumetoken$", Compressed: true},
			flagsInclude: []string{"-t", "$theresumetoken$"},
			exactMatch:   true,
		},
	}

	for testName, test := range sendTests {
		t.Run(testName, func(t *testing.T) {
			flags := test.conf.buildSendFlagsUnchecked()
			assert.GreaterOrEqual(t, len(flags), len(test.flagsInclude))
			assert.Subset(t, flags, test.flagsInclude)
			if test.exactMatch {
				assert.Equal(t, flags, test.flagsInclude)
			}
			for flag := range flags {
				assert.NotContains(t, test.flagsExclude, flag)
			}
		})
	}
}

func TestZFSCommonRecvArgsBuild(t *testing.T) {
	type RecvTest struct {
		conf         RecvOptions
		flagsInclude []string
		flagsExclude []string
	}
	recvTests := map[string]RecvTest{
		"Empty Args": {
			conf:         RecvOptions{},
			flagsInclude: []string{},
			flagsExclude: []string{"-x", "-o", "-F", "-s"},
		},
		"ForceRollback": {
			conf:         RecvOptions{RollbackAndForceRecv: true},
			flagsInclude: []string{"-F"},
			flagsExclude: []string{"-x", "-o", "-s"},
		},
		"PartialSend": {
			conf:         RecvOptions{SavePartialRecvState: true},
			flagsInclude: []string{"-s"},
			flagsExclude: []string{"-x", "-o", "-F"},
		},
		"Override properties": {
			conf:         RecvOptions{OverrideProperties: map[zfsprop.Property]string{zfsprop.Property("abc"): "123"}},
			flagsInclude: []string{"-o", "abc=123"},
			flagsExclude: []string{"-x", "-F", "-s"},
		},
		"Exclude/inherit properties": {
			conf:         RecvOptions{InheritProperties: []zfsprop.Property{"abc", "123"}},
			flagsInclude: []string{"-x", "abc", "123"}, flagsExclude: []string{"-o", "-F", "-s"},
		},
	}

	for testName, test := range recvTests {
		t.Run(testName, func(t *testing.T) {
			flags := test.conf.buildRecvFlags()
			assert.Subset(t, flags, test.flagsInclude)
			for flag := range flags {
				assert.NotContains(t, test.flagsExclude, flag)
			}
		})
	}
}

func TestSendStream_Close_afterRead(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	const foobar = "foobar"
	cmd := zfscmd.CommandContext(ctx, "echo", "-n", foobar)
	var stderrBuf bytes.Buffer
	pipeReader, err := cmd.PipeTo(nil, nil, &stderrBuf)
	require.NoError(t, err)
	require.NoError(t, cmd.Start())

	stream := NewSendStream(cmd, pipeReader, &stderrBuf, cancel)
	stream.testMode = true
	var stdout bytes.Buffer
	n, err := io.Copy(&stdout, stream)
	require.NoError(t, err)
	assert.Equal(t, int64(len(foobar)), n)
	assert.Equal(t, foobar, stdout.String())

	require.NoError(t, stream.Close())
	assert.Empty(t, stderrBuf.Bytes())
}

func TestSendStream_Close_noRead(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	cmd := zfscmd.CommandContext(ctx, "seq", "1024")
	var stderrBuf bytes.Buffer
	pipeReader, err := cmd.PipeTo(nil, nil, &stderrBuf)
	require.NoError(t, err)
	require.NoError(t, cmd.Start())

	stream := NewSendStream(cmd, pipeReader, &stderrBuf, cancel)
	stream.testMode = true
	var zfsError *ZFSError
	require.ErrorAs(t, stream.Close(), &zfsError)
	t.Log(zfsError)
	assert.Contains(t, zfsError.Error(), "signal: broken pipe")
	assert.Empty(t, stderrBuf.Bytes())
}

func TestZFSSendArgsValidated_fromToAbs(t *testing.T) {
	tests := []struct {
		name string
		args ZFSSendArgsValidated
		from string
		to   string
	}{
		{
			name: "with To",
			args: ZFSSendArgsValidated{
				ZFSSendArgsUnvalidated: ZFSSendArgsUnvalidated{
					FS: "zroot/zrepl",
					To: &ZFSSendArgVersion{RelName: "@snap"},
				},
			},
			to: "zroot/zrepl@snap",
		},
		{
			name: "with From and To",
			args: ZFSSendArgsValidated{
				ZFSSendArgsUnvalidated: ZFSSendArgsUnvalidated{
					FS:   "zroot/zrepl",
					From: &ZFSSendArgVersion{RelName: "@snap1"},
					To:   &ZFSSendArgVersion{RelName: "@snap2"},
				},
			},
			from: "zroot/zrepl@snap1",
			to:   "zroot/zrepl@snap2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			from, to, err := tt.args.fromToAbs()
			require.NoError(t, err)
			assert.Equal(t, tt.from, from)
			assert.Equal(t, tt.to, to)
		})
	}
}

func TestZFSSendArgsValidated_env(t *testing.T) {
	const resumeToken = "123"

	tests := []struct {
		name string
		args ZFSSendArgsValidated
		env  map[string]string
	}{
		{
			name: "with ResumeToken",
			args: ZFSSendArgsValidated{
				ZFSSendArgsUnvalidated: ZFSSendArgsUnvalidated{
					ZFSSendFlags: ZFSSendFlags{
						ResumeToken: resumeToken,
					},
				},
			},
			env: map[string]string{"ZREPL_SEND_RESUME_TOKEN": resumeToken},
		},
		{
			name: "with To",
			args: ZFSSendArgsValidated{
				ZFSSendArgsUnvalidated: ZFSSendArgsUnvalidated{
					FS: "zroot/zrepl",
					To: &ZFSSendArgVersion{RelName: "@snap"},
				},
			},
			env: map[string]string{
				"ZREPL_SEND_FROM":     "",
				"ZREPL_SEND_SNAPSHOT": "zroot/zrepl@snap",
			},
		},
		{
			name: "with From and To",
			args: ZFSSendArgsValidated{
				ZFSSendArgsUnvalidated: ZFSSendArgsUnvalidated{
					FS:   "zroot/zrepl",
					From: &ZFSSendArgVersion{RelName: "@snap1"},
					To:   &ZFSSendArgVersion{RelName: "@snap2"},
				},
			},
			env: map[string]string{
				"ZREPL_SEND_FROM":     "zroot/zrepl@snap1",
				"ZREPL_SEND_SNAPSHOT": "zroot/zrepl@snap2",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, err := tt.args.env()
			require.NoError(t, err)
			assert.Equal(t, tt.env, env)
		})
	}
}
