package proxy

type Route struct {
	Rules    []HTTPRule `json:"rules,omitempty"`
	Hostname string     `json:"hostname"`
	Service  string     `json:"service"`
	Port     uint16     `json:"port"`
}

type PodEndpoint struct {
	Address  string `json:"address"`
	PodUID   string `json:"pod_uid"`
	NodeName string `json:"node_name"`
	NodeUID  string `json:"node_uid"`
}

type Backend struct {
	Endpoints   []PodEndpoint `json:"endpoints,omitempty"`
	Key         string        `json:"key"`
	ServiceName string        `json:"service_name"`
	Service     string        `json:"service"`
	Port        uint16        `json:"port"`
	Address     string        `json:"address,omitempty"`
	Weight      int           `json:"weight"`
}
type Match struct {
	Path    string            `json:"path,omitempty"`
	Exact   bool              `json:"exact,omitempty"`
	Method  string            `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Query   map[string]string `json:"query,omitempty"`
}
type HeaderFilter struct {
	Set    map[string]string `json:"set,omitempty"`
	Add    map[string]string `json:"add,omitempty"`
	Remove []string          `json:"remove,omitempty"`
}
type Redirect struct {
	Scheme string `json:"scheme,omitempty"`
	Host   string `json:"host,omitempty"`
	Port   uint16 `json:"port,omitempty"`
	Status int    `json:"status"`
}
type PrefixRewrite struct {
	ReplacePrefix string `json:"replace_prefix"`
}

type HTTPRule struct {
	Rewrite               *PrefixRewrite `json:"rewrite,omitempty"`
	BackendRequestTimeout string         `json:"backend_request_timeout,omitempty"`
	Matches               []Match        `json:"matches,omitempty"`
	Backends              []Backend      `json:"backends,omitempty"`
	RequestHeaders        HeaderFilter   `json:"request_headers,omitempty"`
	ResponseHeaders       HeaderFilter   `json:"response_headers,omitempty"`
	Redirect              *Redirect      `json:"redirect,omitempty"`
	RequestTimeout        string         `json:"request_timeout,omitempty"`
	BackendTimeout        string         `json:"backend_timeout,omitempty"`
	MaxRequestBodyBytes   int64          `json:"max_request_body_bytes,omitempty"`
	Priority              int            `json:"priority,omitempty"`
}
