package app

import "tailscale.com/ipn"

var BuildVersion = "dev"

type Options struct {
	DatabaseDir          string `json:"-"`
	IdentitySecret       string
	PersistentDir        string
	TSStore              ipn.StateStore `json:"-"`
	RequireJoined        bool
	KubernetesService    string
	KubeNamespace        string
	Installation         string
	ControllerName       string
	BrokerURL            string
	BrokerCAFile         string
	BrokerTokenFile      string
	KubernetesController bool

	BootstrapSecretDir     string
	InstallService         bool
	Kubeconfig             string
	GatewayClass           string
	IngressClass           string
	WatchNamespaces        []string
	ProbeListen            string
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
