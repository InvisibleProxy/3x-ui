package job

import (
	"encoding/json"

	"github.com/mhsanaei/3x-ui/v2/database"
	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/util/json_util"
	"github.com/mhsanaei/3x-ui/v2/web/service"
)

// SyncRoutingRules periodically synchronizes routing rules from database to Xray.
// This ensures Xray routing configuration stays in sync with database changes.
type SyncRoutingRules struct {
	xrayService    *service.XrayService
	settingService service.SettingService
}

// NewSyncRoutingRules creates a new routing sync job instance.
func NewSyncRoutingRules(xrayService *service.XrayService) *SyncRoutingRules {
	return &SyncRoutingRules{
		xrayService: xrayService,
	}
}

// Run performs the routing rules synchronization.
// This job runs every minute to ensure routing rules are up-to-date.
func (j *SyncRoutingRules) Run() {
	logger.Debug("[SYNC ROUTING] Running routing rules sync job")

	// Check if Xray is enabled
	xrayEnabled, err := j.settingService.GetXrayEnabled()
	if err != nil {
		logger.Debug("[SYNC ROUTING] Cannot read xrayEnabled, skipping")
		return
	}

	if !xrayEnabled {
		logger.Debug("[SYNC ROUTING] Xray is disabled, skipping routing sync")
		return
	}

	// Check if XrayAPI is available
	xrayAPI := j.xrayService.GetXrayAPI()
	if xrayAPI == nil {
		logger.Debug("[SYNC ROUTING] XrayAPI is nil, skipping routing sync")
		return
	}

	// Get routing configuration from database
	routerConfig, err := j.getRoutingConfigFromDB()
	if err != nil {
		logger.Warning("[SYNC ROUTING] Failed to get routing config from DB:", err)
		return
	}

	if len(routerConfig) == 0 {
		logger.Debug("[SYNC ROUTING] No routing config found in DB")
		return
	}

	// Parse routing rules
	var routingConfig struct {
		DomainStrategy string            `json:"domainStrategy"`
		Rules          []json.RawMessage `json:"rules"`
	}

	if err := json.Unmarshal(routerConfig, &routingConfig); err != nil {
		logger.Warning("[SYNC ROUTING] Failed to parse routing config:", err)
		return
	}

	logger.Debugf("[SYNC ROUTING] Found %d routing rules in DB", len(routingConfig.Rules))

	if err := xrayAPI.AddRoutingRules(routingConfig.DomainStrategy, routingConfig.Rules, false); err != nil {
		logger.Warning("[SYNC ROUTING] Failed to sync routing rules:", err)
		return
	}

	logger.Info("[SYNC ROUTING] Routing rules sync job completed")
}

// getRoutingConfigFromDB retrieves routing configuration from database.
func (j *SyncRoutingRules) getRoutingConfigFromDB() (json_util.RawMessage, error) {
	db := database.GetDB()

	// Get routing config from settings
	routingBytes, err := database.GetJSON(db, model.KeyRouting)
	if err != nil {
		return nil, err
	}

	return json_util.RawMessage(routingBytes), nil
}
