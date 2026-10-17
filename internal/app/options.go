package app

import "tailscale.com/ipn"

var BuildVersion = "dev"

type Options struct {
	Administrators    []string
	DatabaseDir       string `json:"-"`
	IdentitySecret    string
	PersistentDir     string
	TSStore           ipn.StateStore `json:"-"`
	RequireJoined     bool
	KubernetesService string
	KubeNamespace     string
	Installation      string
	ControllerName    string
	BrokerURL         string
	BrokerCAFile      string
	BrokerTokenFile   string

	BootstrapSecretDir     string
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
	EnrollmentID           string
	EnrollmentCode         string
	LoginServer            string
	AcceptDNS              bool
	Tags                   []string
	Listen                 string
	CertEmail              string
	LoadBalancerHostname   string
	LabelKeys              []string
	DenyNetworks           []string
	ConfigFile             string
	EnrollmentCodeFile     string
	EnrollmentCodeStdin    bool
}
