package config

import (
	"fmt"
	"log/syslog"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/dsh2dsh/zrepl/config/yaml"
	"github.com/dsh2dsh/zrepl/util/datasizeunit"
	zfsprop "github.com/dsh2dsh/zrepl/zfs/property"
	"github.com/go-playground/validator/v10"
)

type ParseFlags uint

const (
	ParseFlagsNone        ParseFlags = 0
	ParseFlagsNoCertCheck ParseFlags = 1 << iota
)

func New() *Config {
	return &Config{Global: NewGlobal()}
}

type Config struct {
	Jobs   []JobEnum `yaml:"jobs,optional" validate:"dive,required"`
	Global *Global   `yaml:"global,optional,fromdefaults"`
}

func (c *Config) Job(name string) (*JobEnum, error) {
	for _, j := range c.Jobs {
		if j.Name() == name {
			return &j, nil
		}
	}
	return nil, fmt.Errorf("job %q not defined in config", name)
}

type JobEnum struct {
	Ret interface{}
}

func (j JobEnum) Name() string {
	var name string
	switch v := j.Ret.(type) {
	case *SnapJob:
		name = v.Name
	case *PushJob:
		name = v.Name
	case *SinkJob:
		name = v.Name
	case *PullJob:
		name = v.Name
	case *SourceJob:
		name = v.Name
	default:
		panic(fmt.Sprintf("unknown job type %T", v))
	}
	return name
}

func NewActiveJob() ActiveJob {
	return ActiveJob{Replication: NewReplication()}
}

type ActiveJob struct {
	Type               string                   `yaml:"type" validate:"required"`
	Name               string                   `yaml:"name" validate:"required"`
	Connect            ConnectEnum              `yaml:"connect" validate:"required"`
	Pruning            PruningSenderReceiver    `yaml:"pruning" validate:"required"`
	Replication        *Replication             `yaml:"replication,optional,fromdefaults"`
	ConflictResolution *ConflictResolution      `yaml:"conflict_resolution,optional,fromdefaults"`
	MonitorSnapshots   MonitorSnapshots         `yaml:"monitor,optional"`
	Interval           PositiveDurationOrManual `yaml:"interval,optional"`
	Cron               string                   `yaml:"cron,optional"`
}

func (self *ActiveJob) CronSpec() string {
	if self.Cron != "" {
		return self.Cron
	} else if self.Interval.Interval > 0 && !self.Interval.Manual {
		return "@every " + self.Interval.Interval.Truncate(time.Second).String()
	}
	return ""
}

type ConflictResolution struct {
	InitialReplication string `yaml:"initial_replication,optional,default=all"`
}

type MonitorSnapshots struct {
	Latest []MonitorSnapshot `yaml:"latest,optional" validate:"dive,required"`
	Oldest []MonitorSnapshot `yaml:"oldest,optional" validate:"dive,required"`
}

type MonitorSnapshot struct {
	Prefix   string        `yaml:"prefix,optional"`
	Warning  time.Duration `yaml:"warning,optional"`
	Critical time.Duration `yaml:"critical" validate:"required"`
}

type PassiveJob struct {
	Type             string           `yaml:"type" validate:"required"`
	Name             string           `yaml:"name" validate:"required"`
	Serve            ServeEnum        `yaml:"serve" validate:"required"`
	MonitorSnapshots MonitorSnapshots `yaml:"monitor,optional"`
}

type SnapJob struct {
	Type             string            `yaml:"type" validate:"required"`
	Name             string            `yaml:"name" validate:"required"`
	Pruning          PruningLocal      `yaml:"pruning,optional"`
	Snapshotting     SnapshottingEnum  `yaml:"snapshotting" validate:"required"`
	Filesystems      FilesystemsFilter `yaml:"filesystems" validate:"required"`
	MonitorSnapshots MonitorSnapshots  `yaml:"monitor,optional"`
}

type SendOptions struct {
	Encrypted        bool `yaml:"encrypted,optional,default=false"`
	Raw              bool `yaml:"raw,optional,default=false"`
	SendProperties   bool `yaml:"send_properties,optional,default=false"`
	BackupProperties bool `yaml:"backup_properties,optional,default=false"`
	LargeBlocks      bool `yaml:"large_blocks,optional,default=false"`
	Compressed       bool `yaml:"compressed,optional,default=false"`
	EmbeddedData     bool `yaml:"embedded_data,optional,default=false"`
	Saved            bool `yaml:"saved,optional,default=false"`

	BandwidthLimit *BandwidthLimit `yaml:"bandwidth_limit,optional,fromdefaults"`
	ExecPipe       [][]string      `yaml:"execpipe,optional"`
}

type RecvOptions struct {
	// Note: we cannot enforce encrypted recv as the ZFS cli doesn't provide a mechanism for it
	// Encrypted bool `yaml:"may_encrypted"`
	// Future:
	// Reencrypt bool `yaml:"reencrypt"`

	Properties *PropertyRecvOptions `yaml:"properties,fromdefaults" validate:"required"`

	BandwidthLimit *BandwidthLimit `yaml:"bandwidth_limit,optional,fromdefaults"`

	Placeholder *PlaceholderRecvOptions `yaml:"placeholder,fromdefaults" validate:"required"`

	ExecPipe [][]string `yaml:"execpipe,optional"`
}

var _ yaml.Unmarshaler = &datasizeunit.Bits{}

type BandwidthLimit struct {
	Max            datasizeunit.Bits `yaml:"max,default=-1 B" validate:"required"`
	BucketCapacity datasizeunit.Bits `yaml:"bucket_capacity,default=128 KiB" validate:"required"`
}

func NewReplication() *Replication {
	return &Replication{OneStep: true}
}

type Replication struct {
	Protection  *ReplicationOptionsProtection  `yaml:"protection,optional,fromdefaults"`
	Concurrency *ReplicationOptionsConcurrency `yaml:"concurrency,optional,fromdefaults"`
	OneStep     bool                           `yaml:"one_step,optional"`
}

type ReplicationOptionsProtection struct {
	Initial     string `yaml:"initial,optional,default=guarantee_resumability"`
	Incremental string `yaml:"incremental,optional,default=guarantee_resumability"`
}

type ReplicationOptionsConcurrency struct {
	Steps         int `yaml:"steps,optional,default=1"`
	SizeEstimates int `yaml:"size_estimates,optional,default=4"`
}

type PropertyRecvOptions struct {
	Inherit  []zfsprop.Property          `yaml:"inherit,optional"`
	Override map[zfsprop.Property]string `yaml:"override,optional"`
}

type PlaceholderRecvOptions struct {
	Encryption string `yaml:"encryption,default=inherit" validate:"required"`
}

func NewPushJob() *PushJob {
	return &PushJob{ActiveJob: NewActiveJob()}
}

type PushJob struct {
	ActiveJob    `yaml:",inline"`
	Snapshotting SnapshottingEnum  `yaml:"snapshotting" validate:"required"`
	Filesystems  FilesystemsFilter `yaml:"filesystems" validate:"required"`
	Send         *SendOptions      `yaml:"send,fromdefaults,optional"`
}

func (j *PushJob) GetFilesystems() FilesystemsFilter { return j.Filesystems }
func (j *PushJob) GetSendOptions() *SendOptions      { return j.Send }

func NewPullJob() *PullJob {
	return &PullJob{ActiveJob: NewActiveJob()}
}

type PullJob struct {
	ActiveJob `yaml:",inline"`
	RootFS    string       `yaml:"root_fs" validate:"required"`
	Recv      *RecvOptions `yaml:"recv,fromdefaults,optional"`
}

func (j *PullJob) GetRootFS() string             { return j.RootFS }
func (j *PullJob) GetAppendClientIdentity() bool { return false }
func (j *PullJob) GetRecvOptions() *RecvOptions  { return j.Recv }

type PositiveDurationOrManual struct {
	Interval time.Duration
	Manual   bool
}

var _ yaml.Unmarshaler = (*PositiveDurationOrManual)(nil)

func (i *PositiveDurationOrManual) UnmarshalYAML(u func(interface{}, bool) error) (err error) {
	var s string
	if err := u(&s, true); err != nil {
		return err
	}
	switch s {
	case "manual":
		i.Manual = true
		i.Interval = 0
	case "":
		return fmt.Errorf("value must not be empty")
	default:
		i.Manual = false
		i.Interval, err = parsePositiveDuration(s)
		if err != nil {
			return err
		}
	}
	return nil
}

type SinkJob struct {
	PassiveJob `yaml:",inline"`
	RootFS     string       `yaml:"root_fs" validate:"required"`
	Recv       *RecvOptions `yaml:"recv,optional,fromdefaults"`
}

func (j *SinkJob) GetRootFS() string             { return j.RootFS }
func (j *SinkJob) GetAppendClientIdentity() bool { return true }
func (j *SinkJob) GetRecvOptions() *RecvOptions  { return j.Recv }

type SourceJob struct {
	PassiveJob   `yaml:",inline"`
	Snapshotting SnapshottingEnum  `yaml:"snapshotting" validate:"required"`
	Filesystems  FilesystemsFilter `yaml:"filesystems" validate:"required"`
	Send         *SendOptions      `yaml:"send,optional,fromdefaults"`
}

func (j *SourceJob) GetFilesystems() FilesystemsFilter { return j.Filesystems }
func (j *SourceJob) GetSendOptions() *SendOptions      { return j.Send }

type FilesystemsFilter map[string]bool

type SnapshottingEnum struct {
	Ret interface{}
}

type SnapshottingPeriodic struct {
	Type            string   `yaml:"type" validate:"required"`
	Prefix          string   `yaml:"prefix" validate:"required"`
	Interval        Duration `yaml:"interval,optional"`
	Cron            string   `yaml:"cron,optional"`
	Hooks           HookList `yaml:"hooks,optional"`
	TimestampFormat string   `yaml:"timestamp_format,optional,default=dense"`
	TimestampLocal  bool     `yaml:"timestamp_local,optional"`
}

func (self *SnapshottingPeriodic) CronSpec() string {
	if self.Cron != "" {
		return self.Cron
	} else if self.Interval.Duration() > 0 {
		return "@every " + self.Interval.Duration().Truncate(time.Second).String()
	}
	return ""
}

type SnapshottingManual struct {
	Type string `yaml:"type" validate:"required"`
}

type PruningSenderReceiver struct {
	KeepSender   []PruningEnum `yaml:"keep_sender,optional" validate:"dive,required"`
	KeepReceiver []PruningEnum `yaml:"keep_receiver,optional" validate:"dive,required"`
}

type PruningLocal struct {
	Keep []PruningEnum `yaml:"keep" validate:"dive,required"`
}

type LoggingOutletEnumList []LoggingOutletEnum

func (l *LoggingOutletEnumList) SetDefault() {
	def := `
type: "stdout"
time: true
level: "warn"
format: "human"
`
	s := &StdoutLoggingOutlet{}
	err := yaml.UnmarshalStrict([]byte(def), &s)
	if err != nil {
		panic(err)
	}
	*l = []LoggingOutletEnum{{Ret: s}}
}

var _ yaml.Defaulter = &LoggingOutletEnumList{}

func NewGlobal() *Global {
	return &Global{RpcTimeout: time.Minute, ZfsBin: "zfs"}
}

type Global struct {
	RpcTimeout time.Duration `yaml:"rpc_timeout,optional"`
	ZfsBin     string        `yaml:"zfs_bin,optional"`

	Logging    *LoggingOutletEnumList `yaml:"logging,optional,fromdefaults"`
	Monitoring []MonitoringEnum       `yaml:"monitoring,optional"`
	Control    *GlobalControl         `yaml:"control,optional,fromdefaults"`
	Serve      *GlobalServe           `yaml:"serve,optional,fromdefaults"`
}

type ConnectEnum struct {
	Ret interface{}
}

type ConnectCommon struct {
	Type string `yaml:"type" validate:"required"`
}

type TCPConnect struct {
	ConnectCommon `yaml:",inline"`
	Address       string        `yaml:"address" validate:"required,hostname_port"`
	DialTimeout   time.Duration `yaml:"dial_timeout,default=10s" validate:"min=0s"`
}

type TLSConnect struct {
	ConnectCommon `yaml:",inline"`
	Address       string        `yaml:"address" validate:"required,hostname_port"`
	Ca            string        `yaml:"ca" validate:"required"`
	Cert          string        `yaml:"cert" validate:"required"`
	Key           string        `yaml:"key" validate:"required"`
	ServerCN      string        `yaml:"server_cn" validate:"required"`
	DialTimeout   time.Duration `yaml:"dial_timeout,default=10s" validate:"min=0s"`
}

type SSHStdinserverConnect struct {
	ConnectCommon        `yaml:",inline"`
	Host                 string        `yaml:"host" validate:"required"`
	User                 string        `yaml:"user" validate:"required"`
	Port                 uint16        `yaml:"port" validate:"required"`
	IdentityFile         string        `yaml:"identity_file" validate:"required"`
	TransportOpenCommand []string      `yaml:"transport_open_command,optional"` // TODO unused
	SSHCommand           string        `yaml:"ssh_command,optional"`            // TODO unused
	Options              []string      `yaml:"options,optional"`
	DialTimeout          time.Duration `yaml:"dial_timeout,zeropositive,default=10s" validate:"required"`
}

type LocalConnect struct {
	ConnectCommon  `yaml:",inline"`
	ListenerName   string        `yaml:"listener_name" validate:"required"`
	ClientIdentity string        `yaml:"client_identity" validate:"required"`
	DialTimeout    time.Duration `yaml:"dial_timeout,default=2s" validate:"min=0s"`
}

type ServeEnum struct {
	Ret interface{}
}

type ServeCommon struct {
	Type string `yaml:"type" validate:"required"`
}

type TCPServe struct {
	ServeCommon    `yaml:",inline"`
	Listen         string            `yaml:"listen" validate:"required,hostname_port"`
	ListenFreeBind bool              `yaml:"listen_freebind,default=false"`
	Clients        map[string]string `yaml:"clients" validate:"dive,required"`
}

type TLSServe struct {
	ServeCommon      `yaml:",inline"`
	Listen           string        `yaml:"listen" validate:"required,hostname_port"`
	ListenFreeBind   bool          `yaml:"listen_freebind,default=false"`
	Ca               string        `yaml:"ca" validate:"required"`
	Cert             string        `yaml:"cert" validate:"required"`
	Key              string        `yaml:"key" validate:"required"`
	ClientCNs        []string      `yaml:"client_cns" validate:"dive,required"`
	HandshakeTimeout time.Duration `yaml:"handshake_timeout,default=10s" validate:"min=0s"`
}

type StdinserverServer struct {
	ServeCommon      `yaml:",inline"`
	ClientIdentities []string `yaml:"client_identities" validate:"dive,required"`
}

type LocalServe struct {
	ServeCommon  `yaml:",inline"`
	ListenerName string `yaml:"listener_name" validate:"required"`
}

type PruningEnum struct {
	Ret interface{}
}

type PruneKeepNotReplicated struct {
	Type                 string `yaml:"type" validate:"required"`
	KeepSnapshotAtCursor bool   `yaml:"keep_snapshot_at_cursor,optional,default=true"`
}

type PruneKeepLastN struct {
	Type  string `yaml:"type" validate:"required"`
	Count int    `yaml:"count" validate:"required"`
	Regex string `yaml:"regex,optional"`
}

type PruneKeepRegex struct { // FIXME rename to KeepRegex
	Type   string `yaml:"type" validate:"required"`
	Regex  string `yaml:"regex" validate:"required"`
	Negate bool   `yaml:"negate,optional,default=false"`
}

type LoggingOutletEnum struct {
	Ret interface{}
}

type LoggingOutletCommon struct {
	Type       string   `yaml:"type" validate:"required"`
	Level      string   `yaml:"level" validate:"required"`
	Format     string   `yaml:"format" validate:"required"`
	HideFields []string `yaml:"hide_fields,optional"`
	Time       bool     `yaml:"time,default=true"`
}

type FileLoggingOutlet struct {
	LoggingOutletCommon `yaml:",inline"`
	FileName            string `yaml:"filename,optional"`
}

type StdoutLoggingOutlet struct {
	LoggingOutletCommon `yaml:",inline"`
	Color               bool `yaml:"color,default=true"`
}

type SyslogLoggingOutlet struct {
	LoggingOutletCommon `yaml:",inline"`
	Facility            *SyslogFacility `yaml:"facility,optional,fromdefaults"`
	RetryInterval       time.Duration   `yaml:"retry_interval,default=10s" validate:"gt=0s"`
}

type TCPLoggingOutlet struct {
	LoggingOutletCommon `yaml:",inline"`
	Address             string               `yaml:"address" validate:"required,hostname_port"`
	Net                 string               `yaml:"net,default=tcp" validate:"required"`
	RetryInterval       time.Duration        `yaml:"retry_interval,default=10s" validate:"gt=0s"`
	TLS                 *TCPLoggingOutletTLS `yaml:"tls,optional"`
}

type TCPLoggingOutletTLS struct {
	CA   string `yaml:"ca" validate:"required"`
	Cert string `yaml:"cert" validate:"required"`
	Key  string `yaml:"key" validate:"required"`
}

type MonitoringEnum struct {
	Ret interface{}
}

type PrometheusMonitoring struct {
	Type           string `yaml:"type" validate:"required"`
	Listen         string `yaml:"listen" validate:"required,hostname_port"`
	ListenFreeBind bool   `yaml:"listen_freebind,default=false" validate:"required"`
}

type SyslogFacility syslog.Priority

func (f *SyslogFacility) SetDefault() {
	*f = SyslogFacility(syslog.LOG_LOCAL0)
}

var _ yaml.Defaulter = (*SyslogFacility)(nil)

type GlobalControl struct {
	SockPath string `yaml:"sockpath,default=/var/run/zrepl/control" validate:"required"`
}

type GlobalServe struct {
	StdinServer *GlobalStdinServer `yaml:"stdinserver,optional,fromdefaults"`
}

type GlobalStdinServer struct {
	SockDir string `yaml:"sockdir,default=/var/run/zrepl/stdinserver" validate:"required"`
}

type HookList []HookEnum

type HookEnum struct {
	Ret interface{}
}

type HookCommand struct {
	Path               string            `yaml:"path" validate:"required"`
	Timeout            time.Duration     `yaml:"timeout,optional,default=30s" validate:"gt=0s"`
	Filesystems        FilesystemsFilter `yaml:"filesystems,optional,default={'<': true}"`
	HookSettingsCommon `yaml:",inline"`
}

type HookSettingsCommon struct {
	Type       string `yaml:"type" validate:"required"`
	ErrIsFatal bool   `yaml:"err_is_fatal,optional,default=false"`
}

func enumUnmarshal(u func(interface{}, bool) error, types map[string]interface{}) (interface{}, error) {
	var in struct {
		Type string
	}
	if err := u(&in, true); err != nil {
		return nil, err
	}
	if in.Type == "" {
		return nil, &yaml.TypeError{Errors: []string{"must specify type"}}
	}

	v, ok := types[in.Type]
	if !ok {
		return nil, &yaml.TypeError{Errors: []string{fmt.Sprintf("invalid type name %q", in.Type)}}
	}
	if err := u(v, false); err != nil {
		return nil, err
	}
	return v, nil
}

func (t *JobEnum) UnmarshalYAML(u func(interface{}, bool) error) (err error) {
	t.Ret, err = enumUnmarshal(u, map[string]interface{}{
		"snap":   &SnapJob{},
		"push":   NewPushJob(),
		"sink":   &SinkJob{},
		"pull":   NewPullJob(),
		"source": &SourceJob{},
	})
	return
}

func (t *ConnectEnum) UnmarshalYAML(u func(interface{}, bool) error) (err error) {
	t.Ret, err = enumUnmarshal(u, map[string]interface{}{
		"tcp":             &TCPConnect{},
		"tls":             &TLSConnect{},
		"ssh+stdinserver": &SSHStdinserverConnect{},
		"local":           &LocalConnect{},
	})
	return
}

func (t *ServeEnum) UnmarshalYAML(u func(interface{}, bool) error) (err error) {
	t.Ret, err = enumUnmarshal(u, map[string]interface{}{
		"tcp":         &TCPServe{},
		"tls":         &TLSServe{},
		"stdinserver": &StdinserverServer{},
		"local":       &LocalServe{},
	})
	return
}

func (t *PruningEnum) UnmarshalYAML(u func(interface{}, bool) error) (err error) {
	t.Ret, err = enumUnmarshal(u, map[string]interface{}{
		"not_replicated": &PruneKeepNotReplicated{},
		"last_n":         &PruneKeepLastN{},
		"grid":           &PruneGrid{},
		"regex":          &PruneKeepRegex{},
	})
	return
}

func (t *SnapshottingEnum) UnmarshalYAML(u func(interface{}, bool) error) (err error) {
	t.Ret, err = enumUnmarshal(u, map[string]interface{}{
		"periodic": &SnapshottingPeriodic{},
		"manual":   &SnapshottingManual{},
		"cron":     &SnapshottingPeriodic{},
	})
	return
}

func (t *LoggingOutletEnum) UnmarshalYAML(u func(interface{}, bool) error) (err error) {
	t.Ret, err = enumUnmarshal(u, map[string]interface{}{
		"file":   &FileLoggingOutlet{},
		"stdout": &StdoutLoggingOutlet{},
		"syslog": &SyslogLoggingOutlet{},
		"tcp":    &TCPLoggingOutlet{},
	})
	return
}

func (t *MonitoringEnum) UnmarshalYAML(u func(interface{}, bool) error) (err error) {
	t.Ret, err = enumUnmarshal(u, map[string]interface{}{
		"prometheus": &PrometheusMonitoring{},
	})
	return
}

func (t *SyslogFacility) UnmarshalYAML(u func(interface{}, bool) error) (err error) {
	var s string
	if err := u(&s, true); err != nil {
		return err
	}
	var level syslog.Priority
	switch s {
	case "kern":
		level = syslog.LOG_KERN
	case "user":
		level = syslog.LOG_USER
	case "mail":
		level = syslog.LOG_MAIL
	case "daemon":
		level = syslog.LOG_DAEMON
	case "auth":
		level = syslog.LOG_AUTH
	case "syslog":
		level = syslog.LOG_SYSLOG
	case "lpr":
		level = syslog.LOG_LPR
	case "news":
		level = syslog.LOG_NEWS
	case "uucp":
		level = syslog.LOG_UUCP
	case "cron":
		level = syslog.LOG_CRON
	case "authpriv":
		level = syslog.LOG_AUTHPRIV
	case "ftp":
		level = syslog.LOG_FTP
	case "local0":
		level = syslog.LOG_LOCAL0
	case "local1":
		level = syslog.LOG_LOCAL1
	case "local2":
		level = syslog.LOG_LOCAL2
	case "local3":
		level = syslog.LOG_LOCAL3
	case "local4":
		level = syslog.LOG_LOCAL4
	case "local5":
		level = syslog.LOG_LOCAL5
	case "local6":
		level = syslog.LOG_LOCAL6
	case "local7":
		level = syslog.LOG_LOCAL7
	default:
		return fmt.Errorf("invalid syslog level: %q", s)
	}
	*t = SyslogFacility(level)
	return nil
}

func (t *HookEnum) UnmarshalYAML(u func(interface{}, bool) error) (err error) {
	t.Ret, err = enumUnmarshal(u, map[string]interface{}{
		"command": &HookCommand{},
	})
	return
}

var ConfigFileDefaultLocations = []string{
	"/etc/zrepl/zrepl.yml",
	"/usr/local/etc/zrepl/zrepl.yml",
}

func ParseConfig(path string) (i *Config, err error) {
	if path == "" {
		// Try default locations
		for _, l := range ConfigFileDefaultLocations {
			stat, statErr := os.Stat(l)
			if statErr != nil {
				continue
			}
			if !stat.Mode().IsRegular() {
				err = fmt.Errorf("file at default location is not a regular file: %s", l)
				return
			}
			path = l
			break
		}
	}

	var bytes []byte

	if bytes, err = os.ReadFile(path); err != nil {
		return
	}

	return ParseConfigBytes(bytes)
}

func ParseConfigBytes(bytes []byte) (*Config, error) {
	c := New()
	if err := yaml.UnmarshalStrict(bytes, &c); err != nil {
		return nil, fmt.Errorf("config unmarshal: %w", err)
	}

	if c == nil {
		// There was no yaml document in the file, deserialize from default.
		// => See TestFromdefaultsEmptyDoc in yaml-config package.
		if err := yaml.UnmarshalStrict([]byte("{}"), &c); err != nil {
			return nil, fmt.Errorf("empty config unmarshal: %w", err)
		}
		if c == nil {
			panic("the fallback to deserialize from `{}` should work")
		}
	}

	if err := Validator().Struct(c); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	return c, nil
}

func Validator() *validator.Validate {
	if validate == nil {
		validate = newValidator()
	}
	return validate
}

var validate *validator.Validate

func newValidator() *validator.Validate {
	validate := validator.New(validator.WithRequiredStructEnabled())
	validate.RegisterTagNameFunc(func(fld reflect.StructField) string {
		name := strings.SplitN(fld.Tag.Get("yaml"), ",", 2)[0]
		// skip if tag key says it should be ignored
		if name == "-" {
			return ""
		}
		return name
	})
	return validate
}
