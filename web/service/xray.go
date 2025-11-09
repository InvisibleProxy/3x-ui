package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/mhsanaei/3x-ui/v2/database"
	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/util/json_util"
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

	inboundConfigs, err := s.BuildInboundsConfig()
	if err != nil {
		return nil, err
	}
	xrayConfig.InboundConfigs = append(xrayConfig.InboundConfigs, inboundConfigs...)

	outboundConfigs, err := s.buildOutboundsConfig()
	if err == nil {
		xrayConfig.OutboundConfigs = append(xrayConfig.OutboundConfigs, outboundConfigs...)
	}

	return xrayConfig, nil
}

// BuildInboundsConfig builds Xray inbound configurations from database.
// Called on Xray start/reload to generate actual configuration.
//
// Business logic (for each enabled inbound):
// 1. Loads all inbounds from DB
// 2. Parses settings JSON and extracts client list
// 3. Filters clients:
//   - Removes clients with enable=false
//   - Removes clients with exceeded traffic (up+down >= total)
//
// 4. Cleans client config (keeps only: email, id, password, flow, method)
// 5. Normalizes flow
// 6. Processes streamSettings:
//   - Removes sensitive fields (settings from tls/reality)
//   - Removes externalProxy
//
// 7. Generates final Xray configuration
//
// Returns: (array of inbound configs, error)
func (s *XrayService) BuildInboundsConfig() ([]xray.InboundConfig, error) {
	inbounds, err := s.inboundService.GetAllInbounds()
	if err != nil {
		return nil, err
	}

	var inboundConfigs []xray.InboundConfig
	for _, inbound := range inbounds {
		if !inbound.Enable {
			continue
		}
		// get settings clients
		settings := map[string]any{}
		json.Unmarshal([]byte(inbound.Settings), &settings)
		clients, ok := settings["clients"].([]any)
		if ok {
			clientStats := inbound.ClientStats

			// Remove clients with exceeded limits or expired
			for i := len(clients) - 1; i >= 0; i-- {
				client := clients[i]
				c := client.(map[string]any)
				email, ok := c["email"].(string)
				if !ok {
					continue
				}

				for _, clientTraffic := range clientStats {
					if clientTraffic.Email == email {
						trafficExceeded := clientTraffic.Total > 0 && (clientTraffic.Up+clientTraffic.Down) >= clientTraffic.Total

						if !clientTraffic.Enable || trafficExceeded {
							logger.Infof("Remove Inbound User %s due to expiration or traffic limit", email)
							clients = append(clients[:i], clients[i+1:]...)
						}
						break
					}
				}
			}

			// Clear client config for additional parameters
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

		// Process stream settings
		if len(inbound.StreamSettings) > 0 {
			var stream map[string]any
			json.Unmarshal([]byte(inbound.StreamSettings), &stream)

			// Remove sensitive fields
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
		inboundConfigs = append(inboundConfigs, *inboundConfig)
	}

	return inboundConfigs, nil
}

// buildOutboundsConfig builds outbound configurations from DB.
func (s *XrayService) buildOutboundsConfig() ([]xray.OutboundConfig, error) {
	outboundService := OutboundService{}
	outbounds, err := outboundService.GetAllOutbounds()
	if err != nil {
		return nil, err
	}

	var outboundConfigs []xray.OutboundConfig
	for _, outbound := range outbounds {
		if !outbound.Enable {
			continue
		}
		outboundConfig := xray.OutboundConfig{
			Tag:            outbound.Tag,
			Protocol:       outbound.Protocol,
			Settings:       json_util.RawMessage(outbound.Settings),
			StreamSettings: json_util.RawMessage(outbound.StreamSettings),
			ProxySettings:  json_util.RawMessage(outbound.ProxySettings),
			Mux:            json_util.RawMessage(outbound.Mux),
		}
		outboundConfigs = append(outboundConfigs, outboundConfig)
	}

	return outboundConfigs, nil
}

// GetTraffic retrieves traffic statistics.
func (s *XrayService) GetTraffic() ([]*xray.Traffic, []*xray.ClientTraffic, error) {
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

// GetVirtualConfig builds virtual config.json from DB (template + all inbounds + all outbounds).
func (s *XrayService) GetVirtualConfig() (string, error) {
	config, err := s.GetXrayConfig()
	if err != nil {
		return "", err
	}

	configBytes, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", err
	}

	return string(configBytes), nil
}

// GetVirtualConfigJSON builds virtual config and returns as map.
func (s *XrayService) GetVirtualConfigJSON() (any, error) {
	config, err := s.GetXrayConfig()
	if err != nil {
		return nil, err
	}

	configBytes, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, err
	}

	var jsonData any
	err = json.Unmarshal(configBytes, &jsonData)
	if err != nil {
		return nil, err
	}

	return jsonData, nil
}

// GetFullXrayConfigFromDB builds complete Xray config from DB sections, inbounds and outbounds.
// This returns the full representation of what's stored in the database.
func (s *XrayService) GetFullXrayConfigFromDB() (any, error) {
	config, err := s.GetXrayConfig()
	if err != nil {
		return nil, err
	}

	configBytes, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, err
	}

	var jsonData any
	err = json.Unmarshal(configBytes, &jsonData)
	if err != nil {
		return nil, err
	}

	return jsonData, nil
}

// ApplyVirtualConfig parses and saves Xray configuration to database.
func (s *XrayService) ApplyVirtualConfig(newConfigStr string) error {
	newConfig := &xray.Config{}
	err := json.Unmarshal([]byte(newConfigStr), newConfig)
	if err != nil {
		return fmt.Errorf("failed to parse configuration: %w", err)
	}

	templateConfig := &xray.Config{
		LogConfig:        newConfig.LogConfig,
		API:              newConfig.API,
		DNSConfig:        newConfig.DNSConfig,
		RouterConfig:     newConfig.RouterConfig,
		Policy:           newConfig.Policy,
		InboundConfigs:   []xray.InboundConfig{},
		OutboundConfigs:  []xray.OutboundConfig{},
		Transport:        newConfig.Transport,
		Stats:            newConfig.Stats,
		Reverse:          newConfig.Reverse,
		FakeDNS:          newConfig.FakeDNS,
		Observatory:      newConfig.Observatory,
		BurstObservatory: newConfig.BurstObservatory,
		Metrics:          newConfig.Metrics,
	}

	for _, inbound := range newConfig.InboundConfigs {
		if inbound.Tag == "api" {
			templateConfig.InboundConfigs = append(templateConfig.InboundConfigs, inbound)
			break
		}
	}

	err = s.SaveXrayConfigSections(templateConfig)
	if err != nil {
		return err
	}

	err = s.syncInboundsFromVirtualConfig(newConfig.InboundConfigs)
	if err != nil {
		return err
	}

	if len(newConfig.OutboundConfigs) > 0 {
		if err := s.syncOutboundsFromVirtualConfig(newConfig.OutboundConfigs); err != nil {
			return err
		}
	}

	if len(newConfig.RouterConfig) > 0 && s.xrayAPI != nil {
		s.syncRoutingRulesFromConfig(newConfig.RouterConfig)
	}

	return nil
}

// SaveXrayConfigSections saves Xray configuration as separate sections in DB atomically.
func (s *XrayService) SaveXrayConfigSections(config *xray.Config) error {
	db := database.GetDB()

	templateBytes, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	sectionsMap := map[string]json_util.RawMessage{
		model.KeyXrayTemplate: json_util.RawMessage(templateBytes),
		model.KeyLog:          config.LogConfig,
		model.KeyAPI:          config.API,
		model.KeyPolicy:       config.Policy,
		model.KeyRouting:      config.RouterConfig,
		model.KeyStats:        config.Stats,
		model.KeyMetrics:      config.Metrics,
		model.KeyTransport:    config.Transport,
		model.KeyDNS:          config.DNSConfig,
		model.KeyReverse:      config.Reverse,
		model.KeyFakeDNS:      config.FakeDNS,
		model.KeyObservatory:  config.Observatory,
		model.KeyBurstObserv:  config.BurstObservatory,
	}

	tx := db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	if err := tx.Error; err != nil {
		return err
	}

	for key, value := range sectionsMap {
		var sectionData []byte
		if len(value) > 0 {
			sectionData = []byte(value)
		} else {
			sectionData = []byte("null")
		}

		if err := database.SetJSON(tx, key, sectionData); err != nil {
			tx.Rollback()
			return err
		}
	}

	return tx.Commit().Error
}

// syncInboundsFromVirtualConfig synchronizes inbounds from virtual config to DB.
func (s *XrayService) syncInboundsFromVirtualConfig(configInbounds []xray.InboundConfig) error {
	dbInbounds, err := s.inboundService.GetAllInbounds()
	if err != nil {
		return err
	}

	dbInboundMap := make(map[string]*model.Inbound)
	for _, inbound := range dbInbounds {
		dbInboundMap[inbound.Tag] = inbound
	}

	configInboundMap := make(map[string]bool)
	for _, inbound := range configInbounds {
		if inbound.Tag != "api" {
			configInboundMap[inbound.Tag] = true
		}
	}

	for tag, dbInbound := range dbInboundMap {
		if !configInboundMap[tag] {
			_, err := s.inboundService.DelInbound(dbInbound.Id)
			if err != nil {
				logger.Warning("Failed to delete inbound:", tag, err)
			}
		}
	}

	return nil
}

// syncOutboundsFromVirtualConfig synchronizes outbounds from virtual config to DB.
func (s *XrayService) syncOutboundsFromVirtualConfig(configOutbounds []xray.OutboundConfig) error {
	outboundService := OutboundService{}
	outboundService.SetXrayAPI(s.xrayAPI)

	dbOutbounds, err := outboundService.GetAllOutbounds()
	if err != nil {
		return err
	}

	dbOutboundMap := make(map[string]*model.Outbound)
	for _, outbound := range dbOutbounds {
		dbOutboundMap[outbound.Tag] = outbound
	}

	configOutboundMap := make(map[string]xray.OutboundConfig)
	for _, outbound := range configOutbounds {
		configOutboundMap[outbound.Tag] = outbound
	}

	// Delete outbounds removed from config
	for tag, dbOutbound := range dbOutboundMap {
		if _, exists := configOutboundMap[tag]; !exists {
			err := outboundService.DelOutbound(dbOutbound.ID)
			if err != nil {
				logger.Warning("Failed to delete outbound:", tag, err)
			}
		}
	}

	// Add or update outbounds
	for tag, configOutbound := range configOutboundMap {
		if dbOutbound, exists := dbOutboundMap[tag]; exists {
			dbOutbound.Protocol = configOutbound.Protocol
			dbOutbound.Settings = string(configOutbound.Settings)
			dbOutbound.StreamSettings = string(configOutbound.StreamSettings)
			dbOutbound.ProxySettings = string(configOutbound.ProxySettings)
			dbOutbound.Mux = string(configOutbound.Mux)
			_, err := outboundService.UpdateOutbound(dbOutbound)
			if err != nil {
				logger.Warning("Failed to update outbound:", tag, err)
			}
		} else {
			newOutbound := &model.Outbound{
				Tag:            tag,
				Protocol:       configOutbound.Protocol,
				Settings:       string(configOutbound.Settings),
				StreamSettings: string(configOutbound.StreamSettings),
				ProxySettings:  string(configOutbound.ProxySettings),
				Mux:            string(configOutbound.Mux),
				Enable:         true,
			}
			_, err := outboundService.AddOutbound(newOutbound)
			if err != nil {
				logger.Warning("Failed to add outbound:", tag, err)
			}
		}
	}

	return nil
}
