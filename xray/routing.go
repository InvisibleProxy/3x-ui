package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mhsanaei/3x-ui/v2/logger"

	routerCommand "github.com/xtls/xray-core/app/router/command"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/infra/conf"
)

// validateRuleTag extracts and validates ruleTag from a routing rule JSON.
func validateRuleTag(rule []byte) (string, error) {
	var ruleMap map[string]interface{}
	if err := json.Unmarshal(rule, &ruleMap); err != nil {
		return "", fmt.Errorf("failed to parse rule JSON: %w", err)
	}

	ruleTag, _ := ruleMap["ruleTag"].(string)
	if ruleTag == "" {
		return "", fmt.Errorf("ruleTag is required and cannot be empty")
	}

	return ruleTag, nil
}

// AddRoutingRules adds or replaces routing rules in Xray.
//
// Example rule JSON:
//
//	{
//	  "ruleTag": "block-ads",
//	  "type": "field",
//	  "domain": ["geosite:category-ads-all"],
//	  "outboundTag": "blocked"
//	}
func (x *XrayAPI) AddRoutingRules(domainStrategy string, rules []json.RawMessage, shouldAppend bool) error {
	for i, rule := range rules {
		if _, err := validateRuleTag(rule); err != nil {
			return fmt.Errorf("rule #%d: %w", i+1, err)
		}
	}

	action := "Adding"
	if !shouldAppend {
		action = "Replacing with"
	}
	logger.Infof("[ROUTING RULES] %s %d rules", action, len(rules))

	routerConfig := &conf.RouterConfig{
		DomainStrategy: &domainStrategy,
		RuleList:       rules,
	}

	config, err := routerConfig.Build()
	if err != nil {
		return fmt.Errorf("failed to build router config: %w", err)
	}

	typedMsg := serial.ToTypedMessage(config)
	if typedMsg == nil {
		return fmt.Errorf("failed to convert config to TypedMessage")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = x.RoutingServiceClient.AddRule(ctx, &routerCommand.AddRuleRequest{
		Config:       typedMsg,
		ShouldAppend: shouldAppend,
	})
	if err != nil {
		return fmt.Errorf("failed to apply routing rules: %w", err)
	}

	logger.Info("[ROUTING RULES] Success")
	return nil
}

// AddRoutingRule adds a single routing rule to Xray.
//
// Example JSON:
//
//	{
//	  "ruleTag": "block-private-ip",
//	  "type": "field",
//	  "ip": ["geoip:private"],
//	  "outboundTag": "blocked"
//	}
func (x *XrayAPI) AddRoutingRule(rule []byte, shouldAppend bool) error {
	logger.Debug("[ADD ROUTING RULE] JSON:", string(rule))

	ruleTag, err := validateRuleTag(rule)
	if err != nil {
		return err
	}

	routerConfig := &conf.RouterConfig{
		RuleList: []json.RawMessage{json.RawMessage(rule)},
	}

	config, err := routerConfig.Build()
	if err != nil {
		return fmt.Errorf("failed to build router config: %w", err)
	}

	typedMsg := serial.ToTypedMessage(config)
	if typedMsg == nil {
		return fmt.Errorf("failed to convert config to TypedMessage")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = x.RoutingServiceClient.AddRule(ctx, &routerCommand.AddRuleRequest{
		Config:       typedMsg,
		ShouldAppend: shouldAppend,
	})
	if err != nil {
		return fmt.Errorf("failed to add routing rule: %w", err)
	}

	logger.Debugf("[ADD ROUTING RULE] Success: %s", ruleTag)
	return nil
}

// RemoveRoutingRule removes a routing rule by its tag.
func (x *XrayAPI) RemoveRoutingRule(ruleTag string) error {
	if ruleTag == "" {
		return fmt.Errorf("ruleTag cannot be empty")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := x.RoutingServiceClient.RemoveRule(ctx, &routerCommand.RemoveRuleRequest{
		RuleTag: ruleTag,
	})
	if err != nil {
		return fmt.Errorf("failed to remove routing rule '%s': %w", ruleTag, err)
	}

	logger.Debugf("[REMOVE ROUTING RULE] Success: %s", ruleTag)
	return nil
}

// TestRoute tests a routing decision based on the provided routing context.
func (x *XrayAPI) TestRoute(ctx *routerCommand.RoutingContext) (*routerCommand.RoutingContext, error) {
	grpcCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := x.RoutingServiceClient.TestRoute(grpcCtx, &routerCommand.TestRouteRequest{
		RoutingContext: ctx,
		FieldSelectors: []string{},
		PublishResult:  false,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to test route: %w", err)
	}

	return result, nil
}

// GetBalancerInfo retrieves information about a load balancer by its tag.
func (x *XrayAPI) GetBalancerInfo(balancerTag string) (*routerCommand.GetBalancerInfoResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := x.RoutingServiceClient.GetBalancerInfo(ctx, &routerCommand.GetBalancerInfoRequest{
		Tag: balancerTag,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get balancer info for '%s': %w", balancerTag, err)
	}

	return result, nil
}

// OverrideBalancerTarget overrides the target outbound for a load balancer.
func (x *XrayAPI) OverrideBalancerTarget(balancerTag, targetTag string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := x.RoutingServiceClient.OverrideBalancerTarget(ctx, &routerCommand.OverrideBalancerTargetRequest{
		BalancerTag: balancerTag,
		Target:      targetTag,
	})
	if err != nil {
		return fmt.Errorf("failed to override balancer target: %w", err)
	}

	logger.Debugf("[OVERRIDE BALANCER] Success: balancer=%s target=%s", balancerTag, targetTag)
	return nil
}
