package app

var BuildVersion = "dev"

type Options struct {
	BootstrapOnly          bool
	Hostname               string
	Tailnet                string
	OAuthClientID          string
	OAuthClientSecret      string
	OAuthClientIDFile      string
	OAuthClientSecretFile  string
	OAuthClientIDStdin     bool
	OAuthClientSecretStdin bool
	NonInteractive         bool
	StateDir               string
	DataDir                string
	ManagementURL          string
	ControlKey             string
	BootstrapURL           string
	EnrollmentID           string
	EnrollmentCode         string
	LoginServer            string
	AcceptDNS              bool
	Tags                   []string
	Listen                 string
	BootstrapListen        string
	CertEmail              string
	LoadBalancerHostname   string
	TailscaleIP            string
	ServiceName            string
	ServicePort            uint16
	ReadyFile              string
	LabelKeys              []string
	DenyNetworks           []string
	Force                  bool
	ConfigFile             string
	EnrollmentCodeFile     string
	EnrollmentCodeStdin    bool
	ServiceAction          string
	JSON                   bool
}
