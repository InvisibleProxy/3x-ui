// Package model defines the database models and data structures used by the 3x-ui panel.
package model

import (
	"fmt"

	"github.com/mhsanaei/3x-ui/v2/util/json_util"
	"github.com/mhsanaei/3x-ui/v2/xray"
)

// Protocol represents the protocol type for Xray inbounds.
type Protocol string

// Protocol constants for different Xray inbound protocols
const (
	VMESS       Protocol = "vmess"
	VLESS       Protocol = "vless"
	Tunnel      Protocol = "tunnel"
	HTTP        Protocol = "http"
	Trojan      Protocol = "trojan"
	Shadowsocks Protocol = "shadowsocks"
	Mixed       Protocol = "mixed"
	WireGuard   Protocol = "wireguard"
)

// User represents a user account in the 3x-ui panel.
type User struct {
	Id       int    `json:"id" gorm:"primaryKey;autoIncrement"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// Inbound represents an Xray inbound configuration with traffic statistics and settings.
type Inbound struct {
	Id                   int                  `json:"id" form:"id" gorm:"primaryKey;autoIncrement"`                                                    // Unique identifier
	UserId               int                  `json:"-"`                                                                                               // Associated user ID
	Up                   int64                `json:"up" form:"up"`                                                                                    // Upload traffic in bytes
	Down                 int64                `json:"down" form:"down"`                                                                                // Download traffic in bytes
	Total                int64                `json:"total" form:"total"`                                                                              // Total traffic limit in bytes
	AllTime              int64                `json:"allTime" form:"allTime" gorm:"default:0"`                                                         // All-time traffic usage
	Remark               string               `json:"remark" form:"remark"`                                                                            // Human-readable remark
	Enable               bool                 `json:"enable" form:"enable" gorm:"index:idx_enable_traffic_reset,priority:1"`                           // Whether the inbound is enabled
	ExpiryTime           int64                `json:"expiryTime" form:"expiryTime"`                                                                    // Expiration timestamp
	TrafficReset         string               `json:"trafficReset" form:"trafficReset" gorm:"default:never;index:idx_enable_traffic_reset,priority:2"` // Traffic reset schedule
	LastTrafficResetTime int64                `json:"lastTrafficResetTime" form:"lastTrafficResetTime" gorm:"default:0"`                               // Last traffic reset timestamp
	ClientStats          []xray.ClientTraffic `gorm:"foreignKey:InboundId;references:Id" json:"clientStats" form:"clientStats"`                        // Client traffic statistics

	// Xray configuration fields
	Listen         string   `json:"listen" form:"listen"`
	Port           int      `json:"port" form:"port"`
	Protocol       Protocol `json:"protocol" form:"protocol"`
	Settings       string   `json:"settings" form:"settings"`
	StreamSettings string   `json:"streamSettings" form:"streamSettings"`
	Tag            string   `json:"tag" form:"tag" gorm:"unique"`
	Sniffing       string   `json:"sniffing" form:"sniffing"`
}

// Outbound represents an Xray outbound configuration.
type Outbound struct {
	ID             int    `json:"id" form:"id" gorm:"primaryKey;autoIncrement"`     // Unique identifier
	Tag            string `json:"tag" form:"tag" gorm:"unique"`                     // Xray tag, unique across configuration
	Protocol       string `json:"protocol" form:"protocol"`                         // Protocol: vless/vmess/trojan/freedom/socks/http
	Security       string `json:"security" form:"security" gorm:"type:varchar(16)"` // Security type: none/tls/reality
	Settings       string `json:"settings" form:"settings"`                         // Protocol-specific settings (JSON)
	StreamSettings string `json:"streamSettings" form:"streamSettings"`             // Transport settings (JSON)
	Mux            string `json:"mux" form:"mux"`                                   // Multiplexing settings (JSON)
	ProxySettings  string `json:"proxySettings" form:"proxySettings"`               // Proxy chain settings (JSON)
	Remark         string `json:"remark" form:"remark"`                             // Human-readable description
	Enable         bool   `json:"enable" form:"enable" gorm:"default:true"`         // Whether the outbound is enabled
}

// OutboundTraffics tracks traffic statistics for Xray outbound connections.
type OutboundTraffics struct {
	OutboundID int      `json:"outboundId" gorm:"primaryKey;not null"`                                                     // Primary key and foreign key to Outbound
	Outbound   Outbound `json:"-" gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;foreignKey:OutboundID;references:ID"` // 1:1 relation with cascade
	Up         int64    `json:"up" form:"up" gorm:"default:0"`
	Down       int64    `json:"down" form:"down" gorm:"default:0"`
	Total      int64    `json:"total" form:"total" gorm:"default:0"`
}

// InboundClientIps stores IP addresses associated with inbound clients for access control.
type InboundClientIps struct {
	Id          int    `json:"id" gorm:"primaryKey;autoIncrement"`
	ClientEmail string `json:"clientEmail" form:"clientEmail" gorm:"unique"`
	Ips         string `json:"ips" form:"ips"`
}

// HistoryOfSeeders tracks which database seeders have been executed to prevent re-running.
type HistoryOfSeeders struct {
	Id         int    `json:"id" gorm:"primaryKey;autoIncrement"`
	SeederName string `json:"seederName" gorm:"unique"`
}

// GenXrayInboundConfig generates an Xray inbound configuration from the Inbound model.
func (i *Inbound) GenXrayInboundConfig() *xray.InboundConfig {
	listen := i.Listen
	if listen != "" {
		listen = fmt.Sprintf("\"%v\"", listen)
	}
	return &xray.InboundConfig{
		Listen:         json_util.RawMessage(listen),
		Port:           i.Port,
		Protocol:       string(i.Protocol),
		Settings:       json_util.RawMessage(i.Settings),
		StreamSettings: json_util.RawMessage(i.StreamSettings),
		Tag:            i.Tag,
		Sniffing:       json_util.RawMessage(i.Sniffing),
	}
}

// GenXrayOutboundConfig generates an Xray outbound configuration from the Outbound model.
func (o *Outbound) GenXrayOutboundConfig() *xray.OutboundConfig {
	return &xray.OutboundConfig{
		Tag:            o.Tag,
		Protocol:       o.Protocol,
		Settings:       json_util.RawMessage(o.Settings),
		StreamSettings: json_util.RawMessage(o.StreamSettings),
		ProxySettings:  json_util.RawMessage(o.ProxySettings),
		Mux:            json_util.RawMessage(o.Mux),
	}
}

// Setting stores key-value configuration settings for the 3x-ui panel.
type Setting struct {
	Id    int    `json:"id" form:"id" gorm:"primaryKey;autoIncrement"`
	Key   string `json:"key" form:"key" gorm:"unique"`
	Value string `json:"value" form:"value"`
}

// Xray configuration section keys stored in Settings table
const (
	KeyLog          = "xray.log"
	KeyAPI          = "xray.api"
	KeyPolicy       = "xray.policy"
	KeyRouting      = "xray.routing"
	KeyStats        = "xray.stats"
	KeyMetrics      = "xray.metrics"
	KeyTransport    = "xray.transport"
	KeyDNS          = "xray.dns"
	KeyReverse      = "xray.reverse"
	KeyFakeDNS      = "xray.fakedns"
	KeyObservatory  = "xray.observatory"
	KeyBurstObserv  = "xray.burstObservatory"
	KeyXrayTemplate = "xray.template" // Template config with api-inbound only
)

// Client represents a client configuration for Xray inbounds with traffic limits and settings.
type Client struct {
	ID         string `json:"id"`                           // Unique client identifier
	Security   string `json:"security"`                     // Security method (e.g., "auto", "aes-128-gcm")
	Password   string `json:"password"`                     // Client password
	Flow       string `json:"flow"`                         // Flow control (XTLS)
	Email      string `json:"email"`                        // Client email identifier
	LimitIP    int    `json:"limitIp"`                      // IP limit for this client
	TotalGB    int64  `json:"totalGB" form:"totalGB"`       // Total traffic limit in GB
	ExpiryTime int64  `json:"expiryTime" form:"expiryTime"` // Expiration timestamp
	Enable     bool   `json:"enable" form:"enable"`         // Whether the client is enabled
	TgID       int64  `json:"tgId" form:"tgId"`             // Telegram user ID for notifications
	SubID      string `json:"subId" form:"subId"`           // Subscription identifier
	Comment    string `json:"comment" form:"comment"`       // Client comment
	Reset      int    `json:"reset" form:"reset"`           // Reset period in days
	CreatedAt  int64  `json:"created_at,omitempty"`         // Creation timestamp
	UpdatedAt  int64  `json:"updated_at,omitempty"`         // Last update timestamp
}
