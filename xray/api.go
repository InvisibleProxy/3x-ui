// Package xray provides client integration with the Xray proxy core.
package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mhsanaei/3x-ui/v2/logger"

	"github.com/xtls/xray-core/app/proxyman/command"
	statsService "github.com/xtls/xray-core/app/stats/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/infra/conf"
	"github.com/xtls/xray-core/proxy/shadowsocks"
	"github.com/xtls/xray-core/proxy/shadowsocks_2022"
	"github.com/xtls/xray-core/proxy/trojan"
	"github.com/xtls/xray-core/proxy/vless"
	"github.com/xtls/xray-core/proxy/vmess"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	apiInboundTag = "api"
)

// XrayAPI is a gRPC client for managing Xray core.
type XrayAPI struct {
	HandlerServiceClient command.HandlerServiceClient
	StatsServiceClient   statsService.StatsServiceClient
	grpcClient           *grpc.ClientConn
}

// NewXrayAPI creates a new XrayAPI client.
// host and port must be valid (validated by caller).
func NewXrayAPI(host string, port int) (*XrayAPI, error) {
	addr := fmt.Sprintf("%s:%d", host, port)
	logger.Info("Connecting to Xray gRPC API at:", addr)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Xray API at %s: %w", addr, err)
	}

	api := &XrayAPI{
		grpcClient:           conn,
		HandlerServiceClient: command.NewHandlerServiceClient(conn),
		StatsServiceClient:   statsService.NewStatsServiceClient(conn),
	}

	logger.Info("Xray connection established")
	return api, nil
}

// IsConnected checks if connection is active.
func (x *XrayAPI) IsConnected() bool {
	if x == nil || x.grpcClient == nil {
		return false
	}
	state := x.grpcClient.GetState()
	return state == connectivity.Ready || state == connectivity.Idle
}

// Close closes the connection.
func (x *XrayAPI) Close() {
	if x.grpcClient != nil {
		logger.Info("Closing Xray connection")
		x.grpcClient.Close()
	}
}

// AddInbound adds a new inbound configuration to Xray.
func (x *XrayAPI) AddInbound(inbound []byte) error {
	conf := new(conf.InboundDetourConfig)
	if err := json.Unmarshal(inbound, conf); err != nil {
		return fmt.Errorf("invalid inbound configuration: %w", err)
	}

	config, err := conf.Build()
	if err != nil {
		return fmt.Errorf("invalid inbound settings: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = x.HandlerServiceClient.AddInbound(ctx, &command.AddInboundRequest{Inbound: config})
	if err != nil {
		return fmt.Errorf("failed to add inbound: %w", err)
	}

	return nil
}

// DelInbound removes an inbound by tag.
func (x *XrayAPI) DelInbound(tag string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := x.HandlerServiceClient.RemoveInbound(ctx, &command.RemoveInboundRequest{Tag: tag})
	if err != nil {
		return fmt.Errorf("failed to remove inbound: %w", err)
	}
	return nil
}

// AddUser adds a user to an inbound.
// Supported protocols: vless, vmess, trojan, shadowsocks, shadowsocks-2022.
func (x *XrayAPI) AddUser(Protocol string, inboundTag string, user map[string]any) error {
	var account *serial.TypedMessage
	switch Protocol {
	case "vmess":
		account = serial.ToTypedMessage(&vmess.Account{
			Id: user["id"].(string),
		})
	case "vless":
		account = serial.ToTypedMessage(&vless.Account{
			Id:   user["id"].(string),
			Flow: user["flow"].(string),
		})
	case "trojan":
		account = serial.ToTypedMessage(&trojan.Account{
			Password: user["password"].(string),
		})
	case "shadowsocks":
		var ssCipherType shadowsocks.CipherType
		switch user["cipher"].(string) {
		case "aes-128-gcm":
			ssCipherType = shadowsocks.CipherType_AES_128_GCM
		case "aes-256-gcm":
			ssCipherType = shadowsocks.CipherType_AES_256_GCM
		case "chacha20-poly1305", "chacha20-ietf-poly1305":
			ssCipherType = shadowsocks.CipherType_CHACHA20_POLY1305
		case "xchacha20-poly1305", "xchacha20-ietf-poly1305":
			ssCipherType = shadowsocks.CipherType_XCHACHA20_POLY1305
		default:
			ssCipherType = shadowsocks.CipherType_NONE
		}

		if ssCipherType != shadowsocks.CipherType_NONE {
			account = serial.ToTypedMessage(&shadowsocks.Account{
				Password:   user["password"].(string),
				CipherType: ssCipherType,
			})
		} else {
			account = serial.ToTypedMessage(&shadowsocks_2022.ServerConfig{
				Key:   user["password"].(string),
				Email: user["email"].(string),
			})
		}
	default:
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := x.HandlerServiceClient.AlterInbound(ctx, &command.AlterInboundRequest{
		Tag: inboundTag,
		Operation: serial.ToTypedMessage(&command.AddUserOperation{
			User: &protocol.User{
				Email:   user["email"].(string),
				Account: account,
			},
		}),
	})
	if err != nil {
		return fmt.Errorf("failed to add user: %w", err)
	}

	return nil
}

// RemoveUser removes a user from an inbound by email.
func (x *XrayAPI) RemoveUser(inboundTag, email string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	op := &command.RemoveUserOperation{Email: email}
	req := &command.AlterInboundRequest{
		Tag:       inboundTag,
		Operation: serial.ToTypedMessage(op),
	}

	_, err := x.HandlerServiceClient.AlterInbound(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to remove user: %w", err)
	}

	return nil
}

// AddOutbound adds a new outbound configuration to Xray.
func (x *XrayAPI) AddOutbound(outbound []byte) error {
	logger.Debug("[ADD OUTBOUND] JSON:", string(outbound))

	conf := new(conf.OutboundDetourConfig)
	if err := json.Unmarshal(outbound, conf); err != nil {
		return fmt.Errorf("invalid outbound configuration: %w", err)
	}

	config, err := conf.Build()
	if err != nil {
		return fmt.Errorf("failed to build outbound: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = x.HandlerServiceClient.AddOutbound(ctx, &command.AddOutboundRequest{Outbound: config})
	if err != nil {
		return fmt.Errorf("failed to add outbound: %w", err)
	}

	logger.Debug("[ADD OUTBOUND] Success:", conf.Tag)
	return nil
}

// DelOutbound removes an outbound by tag.
func (x *XrayAPI) DelOutbound(tag string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := x.HandlerServiceClient.RemoveOutbound(ctx, &command.RemoveOutboundRequest{Tag: tag})
	if err != nil {
		return fmt.Errorf("failed to remove outbound: %w", err)
	}
	return nil
}

// ListInboundTags returns all currently running inbound tags.
func (x *XrayAPI) ListInboundTags() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := x.HandlerServiceClient.ListInbounds(ctx, &command.ListInboundsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to get inbound list: %w", err)
	}

	var tags []string
	if resp != nil && resp.Inbounds != nil {
		for _, inbound := range resp.Inbounds {
			if inbound != nil && inbound.Tag != "" {
				tags = append(tags, inbound.Tag)
			}
		}
	}
	return tags, nil
}

// ListOutboundTags returns all currently running outbound tags.
func (x *XrayAPI) ListOutboundTags() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := x.HandlerServiceClient.ListOutbounds(ctx, &command.ListOutboundsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to get outbound list: %w", err)
	}

	var tags []string
	if resp != nil && resp.Outbounds != nil {
		for _, outbound := range resp.Outbounds {
			if outbound != nil && outbound.Tag != "" {
				tags = append(tags, outbound.Tag)
			}
		}
	}
	return tags, nil
}

// GetTraffic retrieves traffic statistics, optionally resetting counters.
func (x *XrayAPI) GetTraffic(reset bool) ([]*Traffic, []*ClientTraffic, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := x.StatsServiceClient.QueryStats(ctx, &statsService.QueryStatsRequest{Reset_: reset})
	if err != nil {
		logger.Debug("Failed to query Xray stats:", err)
		return nil, nil, err
	}

	tagTrafficMap := make(map[string]*Traffic)
	emailTrafficMap := make(map[string]*ClientTraffic)

	for _, stat := range resp.GetStat() {
		processStatistic(stat.Name, stat.Value, tagTrafficMap, emailTrafficMap)
	}
	return mapToSlice(tagTrafficMap), mapToSlice(emailTrafficMap), nil
}

// processStatistic parses and aggregates a single stat entry.
// Stat name format: "inbound>>>tag>>>traffic>>>uplink" or "user>>>email>>>traffic>>>downlink"
func processStatistic(name string, value int64, trafficMap map[string]*Traffic, clientTrafficMap map[string]*ClientTraffic) {
	parts := splitStatName(name)
	if len(parts) < 4 {
		return
	}

	// Check stat type: inbound/outbound/user
	switch parts[0] {
	case "inbound", "outbound":
		processInboundOutboundTraffic(parts, value, trafficMap)
	case "user":
		processClientTraffic(parts, value, clientTrafficMap)
	}
}

// splitStatName splits stat name by ">>>" separator (faster than regex)
func splitStatName(name string) []string {
	parts := make([]string, 0, 4)
	start := 0

	for i := 0; i < len(name)-2; i++ {
		if name[i] == '>' && name[i+1] == '>' && name[i+2] == '>' {
			parts = append(parts, name[start:i])
			start = i + 3
			i += 2 // Skip '>>'
		}
	}
	// Add last part
	if start < len(name) {
		parts = append(parts, name[start:])
	}
	return parts
}

// processInboundOutboundTraffic aggregates inbound/outbound traffic statistics.
// parts: ["inbound/outbound", "tag", "traffic", "uplink/downlink"]
func processInboundOutboundTraffic(parts []string, value int64, trafficMap map[string]*Traffic) {
	if len(parts) != 4 || parts[2] != "traffic" {
		return
	}

	tag := parts[1]
	if tag == apiInboundTag {
		return
	}

	isInbound := parts[0] == "inbound"
	isDown := parts[3] == "downlink"

	traffic, ok := trafficMap[tag]
	if !ok {
		traffic = &Traffic{
			IsInbound:  isInbound,
			IsOutbound: !isInbound,
			Tag:        tag,
		}
		trafficMap[tag] = traffic
	}

	if isDown {
		traffic.Down = value
	} else {
		traffic.Up = value
	}
}

// processClientTraffic aggregates client traffic statistics.
// parts: ["user", "email", "traffic", "uplink/downlink"]
func processClientTraffic(parts []string, value int64, clientTrafficMap map[string]*ClientTraffic) {
	if len(parts) != 4 || parts[2] != "traffic" {
		return
	}

	email := parts[1]
	isDown := parts[3] == "downlink"

	traffic, ok := clientTrafficMap[email]
	if !ok {
		traffic = &ClientTraffic{Email: email}
		clientTrafficMap[email] = traffic
	}

	if isDown {
		traffic.Down = value
	} else {
		traffic.Up = value
	}
}

// mapToSlice converts a map of pointers to a slice of pointers.
func mapToSlice[T any](m map[string]*T) []*T {
	result := make([]*T, 0, len(m))
	for _, v := range m {
		result = append(result, v)
	}
	return result
}
