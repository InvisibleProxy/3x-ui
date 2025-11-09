package service

import (
	"encoding/json"
	"fmt"

	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/util/json_util"
)

// syncRoutingRulesFromConfig synchronizes routing rules from RouterConfig to running Xray instance.
func (s *XrayService) syncRoutingRulesFromConfig(routerConfigJSON json_util.RawMessage) error {
	if s.xrayAPI == nil {
		return fmt.Errorf("xray API is not available")
	}

	logger.Info("[SYNC ROUTING] Syncing routing rules with Xray")

	var routingConfig struct {
		DomainStrategy string            `json:"domainStrategy"`
		Rules          []json.RawMessage `json:"rules"`
	}

	if err := json.Unmarshal(routerConfigJSON, &routingConfig); err != nil {
		return fmt.Errorf("failed to parse routing config: %w", err)
	}

	if err := s.xrayAPI.AddRoutingRules(routingConfig.DomainStrategy, routingConfig.Rules, false); err != nil {
		return fmt.Errorf("failed to sync routing rules: %w", err)
	}

	logger.Info("[SYNC ROUTING] Routing rules synchronized successfully")
	return nil
}

// AddRoutingRule adds a new routing rule to Xray dynamically.
func (s *XrayService) AddRoutingRule(rule []byte, shouldAppend bool) error {
	if s.xrayAPI == nil {
		return fmt.Errorf("xray API is not available")
	}
	return s.xrayAPI.AddRoutingRule(rule, shouldAppend)
}

// RemoveRoutingRule removes a routing rule by its tag from the running Xray instance.
func (s *XrayService) RemoveRoutingRule(ruleTag string) error {
	if s.xrayAPI == nil {
		return fmt.Errorf("xray API is not available")
	}
	return s.xrayAPI.RemoveRoutingRule(ruleTag)
}
