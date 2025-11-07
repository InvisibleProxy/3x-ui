package service

import (
	"encoding/json"
	"errors"
	"os"
	"strconv"

	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/xray"
)

// XrayService provides business logic for Xray management.
type XrayService struct {
	inboundService InboundService
	settingService SettingService
	xrayAPI        *xray.XrayAPI
}

// NewXrayService creates a new XrayService.
func NewXrayService() *XrayService {
	host := getAPIHost()
	port := getAPIPort()

	api, err := xray.NewXrayAPI(host, port)
	if err != nil {
		logger.Warning("Failed to connect to Xray:", err)
		return &XrayService{xrayAPI: nil}
	}

	logger.Info("XrayService initialized")

	svc := &XrayService{xrayAPI: api}
	svc.inboundService.SetXrayAPI(api)

	return svc
}

// GetXrayAPI returns the XrayAPI instance.
func (s *XrayService) GetXrayAPI() *xray.XrayAPI {
	return s.xrayAPI
}

// getAPIHost reads and validates XRAY_HOST from environment.
func getAPIHost() string {
	const defaultHost = "127.0.0.1"

	host := os.Getenv("XRAY_HOST")
	if host == "" {
		return defaultHost
	}
	return host
}

// getAPIPort reads and validates XRAY_PORT from environment.
func getAPIPort() int {
	const defaultPort = 10085

	portStr := os.Getenv("XRAY_PORT")
	if portStr == "" {
		return defaultPort
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		logger.Warning("Invalid XRAY_PORT (not a number), using default:", portStr)
		return defaultPort
	}

	if port <= 0 || port > 65535 {
		logger.Warning("Invalid XRAY_PORT (out of range), using default:", port)
		return defaultPort
	}

	return port
}

// IsXrayRunning checks if the Xray container is accessible.
func (s *XrayService) IsXrayRunning() bool {
	if s.xrayAPI == nil {
		return false
	}
	return s.xrayAPI.IsConnected()
}

// GetXrayVersion returns the Xray core version from environment variable.
func (s *XrayService) GetXrayVersion() string {
	version := os.Getenv("XRAY_CORE_VERSION")
	if version == "" {
		return "Unknown"
	}
	return version
}

// GetXrayConfig retrieves and builds the Xray configuration from settings and inbounds.
func (s *XrayService) GetXrayConfig() (*xray.Config, error) {
	templateConfig, err := s.settingService.GetXrayConfigTemplate()
	if err != nil {
		return nil, err
	}

	xrayConfig := &xray.Config{}
	err = json.Unmarshal([]byte(templateConfig), xrayConfig)
	if err != nil {
		return nil, err
	}

	s.inboundService.AddTraffic(nil, nil)

	inbounds, err := s.inboundService.GetAllInbounds()
	if err != nil {
		return nil, err
	}
	for _, inbound := range inbounds {
		if !inbound.Enable {
			continue
		}
		// get settings clients
		settings := map[string]any{}
		json.Unmarshal([]byte(inbound.Settings), &settings)
		clients, ok := settings["clients"].([]any)
		if ok {
			// check users active or not
			clientStats := inbound.ClientStats
			for _, clientTraffic := range clientStats {
				indexDecrease := 0
				for index, client := range clients {
					c := client.(map[string]any)
					if c["email"] == clientTraffic.Email {
						if !clientTraffic.Enable {
							clients = append(clients[:index-indexDecrease], clients[index-indexDecrease+1:]...)
							indexDecrease++
							logger.Infof("Remove Inbound User %s due to expiration or traffic limit", c["email"])
						}
					}
				}
			}

			// clear client config for additional parameters
			var final_clients []any
			for _, client := range clients {
				c := client.(map[string]any)
				if c["enable"] != nil {
					if enable, ok := c["enable"].(bool); ok && !enable {
						continue
					}
				}
				for key := range c {
					if key != "email" && key != "id" && key != "password" && key != "flow" && key != "method" {
						delete(c, key)
					}
					if c["flow"] == "xtls-rprx-vision-udp443" {
						c["flow"] = "xtls-rprx-vision"
					}
				}
				final_clients = append(final_clients, any(c))
			}

			settings["clients"] = final_clients
			modifiedSettings, err := json.MarshalIndent(settings, "", "  ")
			if err != nil {
				return nil, err
			}

			inbound.Settings = string(modifiedSettings)
		}

		if len(inbound.StreamSettings) > 0 {
			// Unmarshal stream JSON
			var stream map[string]any
			json.Unmarshal([]byte(inbound.StreamSettings), &stream)

			// Remove the "settings" field under "tlsSettings" and "realitySettings"
			tlsSettings, ok1 := stream["tlsSettings"].(map[string]any)
			realitySettings, ok2 := stream["realitySettings"].(map[string]any)
			if ok1 || ok2 {
				if ok1 {
					delete(tlsSettings, "settings")
				} else if ok2 {
					delete(realitySettings, "settings")
				}
			}

			delete(stream, "externalProxy")

			newStream, err := json.MarshalIndent(stream, "", "  ")
			if err != nil {
				return nil, err
			}
			inbound.StreamSettings = string(newStream)
		}

		inboundConfig := inbound.GenXrayInboundConfig()
		xrayConfig.InboundConfigs = append(xrayConfig.InboundConfigs, *inboundConfig)
	}
	return xrayConfig, nil
}

// GetXrayTraffic retrieves traffic statistics.
func (s *XrayService) GetXrayTraffic() ([]*xray.Traffic, []*xray.ClientTraffic, error) {
	if s.xrayAPI == nil {
		return nil, nil, errors.New("xray API not initialized")
	}

	traffic, clientTraffic, err := s.xrayAPI.GetTraffic(true)
	if err != nil {
		logger.Debug("Failed to fetch Xray traffic:", err)
		return nil, nil, err
	}
	return traffic, clientTraffic, nil
}

// CloseConnection closes the Xray connection.
func (s *XrayService) CloseConnection() {
	if s.xrayAPI != nil {
		s.xrayAPI.Close()
	}
}
