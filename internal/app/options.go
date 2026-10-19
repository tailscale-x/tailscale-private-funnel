package app

import (
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

var BuildVersion = "dev"

type Options struct {
	UsersClientIDFile, UsersClientSecretFile       string
	ServicesClientIDFile, ServicesClientSecretFile string
	MaterialDir                                    string
	BootstrapURL                                   string
	BootstrapTokenFile                             string
	BootstrapCAFile                                string
	NodeUID                                        string
	NodeName                                       string

	TailscaleConnection    string
	TailscaleSocket        string
	RoutingID              string
	ComponentID            string
	InstanceID             string
	ParentID               string
	Ephemeral              bool
	Database               store.DatabaseOptions
	Administrators         []string
	DatabaseDir            string `json:"-"`
	PersistentDir          string
	RequireJoined          bool
	KubernetesService      string
	KubeNamespace          string
	Installation           string
	ControllerName         string
	BrokerURL              string
	BrokerCAFile           string
	BrokerTokenFile        string
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
	LoginServer            string
	AcceptDNS              bool
	Tags                   []string
	Listen                 string
	CertEmail              string
	LoadBalancerHostname   string
	LabelKeys              []string
	DenyNetworks           []string
	ConfigFile             string
}
