package xray

import (
	"bytes"

	"github.com/mhsanaei/3x-ui/v2/util/json_util"
)

// OutboundConfig represents an Xray outbound configuration.
type OutboundConfig struct {
	Tag            string               `json:"tag"`
	Protocol       string               `json:"protocol"`
	Settings       json_util.RawMessage `json:"settings"`
	StreamSettings json_util.RawMessage `json:"streamSettings,omitempty"`
	ProxySettings  json_util.RawMessage `json:"proxySettings,omitempty"`
	Mux            json_util.RawMessage `json:"mux,omitempty"`
}

// Equals compares two OutboundConfig instances for deep equality.
func (o *OutboundConfig) Equals(other *OutboundConfig) bool {
	if o.Tag != other.Tag {
		return false
	}
	if o.Protocol != other.Protocol {
		return false
	}
	if !bytes.Equal(o.Settings, other.Settings) {
		return false
	}
	if !bytes.Equal(o.StreamSettings, other.StreamSettings) {
		return false
	}
	if !bytes.Equal(o.ProxySettings, other.ProxySettings) {
		return false
	}
	if !bytes.Equal(o.Mux, other.Mux) {
		return false
	}
	return true
}
