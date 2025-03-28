package config

import (
	"bufio"
	"bytes"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSampleConfigsAreParsedWithoutErrors(t *testing.T) {
	paths, err := filepath.Glob("./samples/*")
	if err != nil {
		t.Errorf("glob failed: %+v", err)
	}

	for _, p := range paths {

		if path.Ext(p) != ".yml" {
			t.Logf("skipping file %s", p)
			continue
		}

		t.Run(p, func(t *testing.T) {
			c, err := ParseConfig(p)
			if err != nil {
				t.Errorf("error parsing %s:\n%+v", p, err)
			}

			t.Logf("file: %s", p)
			t.Logf("%#v", c)
		})

	}
}

func testValidConfig(t *testing.T, input string) *Config {
	t.Helper()
	conf, err := testConfig(t, input)
	require.NoError(t, err)
	require.NotNil(t, conf)
	return conf
}

func testConfig(t *testing.T, input string) (*Config, error) {
	t.Helper()
	return ParseConfigBytes("", []byte(input))
}

func trimSpaceEachLineAndPad(s, pad string) string {
	var out strings.Builder
	scan := bufio.NewScanner(strings.NewReader(s))
	for scan.Scan() {
		fmt.Fprintf(&out, "%s%s\n", pad, bytes.TrimSpace(scan.Bytes()))
	}
	return out.String()
}

func TestTrimSpaceEachLineAndPad(t *testing.T) {
	foo := `
	foo
	bar baz
	`
	assert.Equal(t, "  \n  foo\n  bar baz\n  \n", trimSpaceEachLineAndPad(foo, "  "))
}

func TestEmptyConfig(t *testing.T) {
	cases := []string{
		"",
		"\n",
		"---",
		"---\n",
	}
	for _, input := range cases {
		_, err := testConfig(t, input)
		t.Log(err)
		require.Error(t, err)
	}
}

func TestPushJob(t *testing.T) {
	c := testValidConfig(t, `
jobs:
  - name: "foo"
    type: "push"
    connect:
      type: "local"
      listener_name: "foo"
      client_identity: "bar"
    filesystems:
      "<": true
    snapshotting:
      type: "manual"
    pruning:
      keep_sender:
        - type: "not_replicated"
`)

	require.NotEmpty(t, c.Jobs)
	pushJob := c.Jobs[0].Ret.(*PushJob)
	require.NotNil(t, pushJob)
	assert.Empty(t, pushJob.Replication.Prefix)
}

func TestPushJob_withPrefix(t *testing.T) {
	c := testValidConfig(t, `
jobs:
  - name: "foo"
    type: "push"
    connect:
      type: "local"
      listener_name: "foo"
      client_identity: "bar"
    filesystems:
      "<": true
    snapshotting:
      type: "manual"
    replication:
      prefix: "zrepl_"
    pruning:
      keep_sender:
        - type: "not_replicated"
`)

	require.NotEmpty(t, c.Jobs)
	pushJob := c.Jobs[0].Ret.(*PushJob)
	require.NotNil(t, pushJob)
	assert.Equal(t, "zrepl_", pushJob.Replication.Prefix)
}

func TestPullJob(t *testing.T) {
	c := testValidConfig(t, `
jobs:
  - name: "foo"
    type: "pull"
    connect:
      type: "http"
      server: "https://server1.foo.bar:8888"
      listener_name: "job_name"
      client_identity: "client_name"
    root_fs: "pool2/backup_servers"
    pruning:
      keep_sender:
        - type: "not_replicated"
`)

	require.NotEmpty(t, c.Jobs)
	pullJob := c.Jobs[0].Ret.(*PullJob)
	require.NotNil(t, pullJob)
	assert.Empty(t, pullJob.Replication.Prefix)
}

func TestPullJob_withPrefix(t *testing.T) {
	c := testValidConfig(t, `
jobs:
  - name: "foo"
    type: "pull"
    connect:
      type: "http"
      server: "https://server1.foo.bar:8888"
      listener_name: "job_name"
      client_identity: "client_name"
    root_fs: "pool2/backup_servers"
    replication:
      prefix: "zrepl_"
    pruning:
      keep_sender:
        - type: "not_replicated"
`)

	require.NotEmpty(t, c.Jobs)
	pullJob := c.Jobs[0].Ret.(*PullJob)
	require.NotNil(t, pullJob)
	assert.Equal(t, "zrepl_", pullJob.Replication.Prefix)
}

func TestSnapshottingPeriodic_TimestampLocal_defaultTrue(t *testing.T) {
	c := testValidConfig(t, `
jobs:
  - name: "foo"
    type: "push"
    connect:
      type: "local"
      listener_name: "foo"
      client_identity: "bar"
    filesystems:
      "<": true
    snapshotting:
      type: "periodic"
      prefix: "zrepl_"
    pruning:
      keep_sender:
        - type: "not_replicated"
`)

	require.NotEmpty(t, c.Jobs)
	require.IsType(t, new(PushJob), c.Jobs[0].Ret)
	pushJob := c.Jobs[0].Ret.(*PushJob)
	require.NotNil(t, pushJob)

	require.IsType(t, new(SnapshottingPeriodic), pushJob.Snapshotting.Ret)
	snap := pushJob.Snapshotting.Ret.(*SnapshottingPeriodic)
	require.NotNil(t, snap)
	assert.True(t, snap.TimestampLocal)
}

func TestSendOptions_Raw_defaultTrue(t *testing.T) {
	c := testValidConfig(t, `
jobs:
  - name: "foo"
    type: "push"
    connect:
      type: "local"
      listener_name: "foo"
      client_identity: "bar"
    filesystems:
      "<": true
    snapshotting:
      type: "periodic"
      prefix: "zrepl_"
    pruning:
      keep_sender:
        - type: "not_replicated"
`)

	require.NotEmpty(t, c.Jobs)
	require.IsType(t, new(PushJob), c.Jobs[0].Ret)
	job := c.Jobs[0].Ret.(*PushJob)
	require.NotNil(t, job)
	assert.True(t, job.Send.Raw)
}
