package proxy

type Route struct {
	Hostname string `json:"hostname"`
	Service  string `json:"service"`
	Port     uint16 `json:"port"`
}
