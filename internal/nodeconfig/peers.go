package nodeconfig

type Peer struct {
	ID        string   `json:"id"`
	Hostname  string   `json:"hostname"`
	DNSName   string   `json:"dns_name"`
	Addresses []string `json:"addresses"`
	Tags      []string `json:"tags"`
	Online    bool     `json:"online"`
}
type Probe struct {
	ID        string `json:"id"`
	DeviceID  string `json:"device_id"`
	Port      uint16 `json:"port"`
	ExpiresAt int64  `json:"expires_at"`
}
type ProbeResult struct {
	ID        string `json:"id"`
	DeviceID  string `json:"device_id"`
	Port      uint16 `json:"port"`
	Reachable bool   `json:"reachable"`
	Error     string `json:"error,omitempty"`
	CheckedAt int64  `json:"checked_at"`
}
type PeerSnapshot struct {
	Sources        []string      `json:"sources"`
	Roles          []string      `json:"roles,omitempty"`
	LoadBalancerID string        `json:"loadbalancer_id,omitempty"`
	Peers          []Peer        `json:"peers"`
	Tags           []string      `json:"tags"`
	Results        []ProbeResult `json:"results"`
	ExpiresAt      int64         `json:"expires_at"`
	ReceivedAt     int64         `json:"received_at"`
}
