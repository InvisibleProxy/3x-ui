package job

import (
	"encoding/json"

	"github.com/mhsanaei/3x-ui/v2/database"
	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/web/service"
	"github.com/mhsanaei/3x-ui/v2/xray"
)

// XrayConfigSyncJob periodically synchronizes Xray configuration from database.
// This ensures Xray always has up-to-date inbound configurations.
type XrayConfigSyncJob struct {
	xrayService *service.XrayService
}

// NewXrayConfigSyncJob creates a new configuration sync job instance.
func NewXrayConfigSyncJob(xrayService *service.XrayService) *XrayConfigSyncJob {
	return &XrayConfigSyncJob{
		xrayService: xrayService,
	}
}

// Run performs the configuration synchronization.
func (j *XrayConfigSyncJob) Run() {
	settingService := service.SettingService{}
	xrayEnabled, err := settingService.GetXrayEnabled()
	if err != nil {
		logger.Debug("[SYNC] Cannot read xrayEnabled, skipping")
		return
	}

	if !xrayEnabled {
		return
	}

	xrayAPI := j.xrayService.GetXrayAPI()
	if xrayAPI == nil {
		return
	}

	j.syncInbounds(xrayAPI)
	j.syncOutbounds(xrayAPI)
}

// syncInbounds synchronizes enabled inbounds from DB to Xray.
func (j *XrayConfigSyncJob) syncInbounds(xrayAPI *xray.XrayAPI) {
	db := database.GetDB()
	var dbInbounds []*model.Inbound
	err := db.Model(&model.Inbound{}).Where("enable = ?", true).Find(&dbInbounds).Error
	if err != nil {
		logger.Warning("[SYNC] Database error:", err)
		return
	}

	if len(dbInbounds) == 0 {
		return
	}

	currentTags, err := xrayAPI.ListInboundTags()
	if err != nil {
		return
	}

	currentTagsMap := make(map[string]bool)
	for _, tag := range currentTags {
		currentTagsMap[tag] = true
	}

	expectedTags := make(map[string]*model.Inbound)
	for _, inbound := range dbInbounds {
		expectedTags[inbound.Tag] = inbound
	}

	addedCount := 0
	failedCount := 0

	for tag, inbound := range expectedTags {
		if currentTagsMap[tag] {
			continue
		}

		inboundJson, err := json.MarshalIndent(inbound.GenXrayInboundConfig(), "", "  ")
		if err != nil {
			failedCount++
			continue
		}

		err = xrayAPI.AddInbound(inboundJson)
		if err == nil {
			addedCount++
		} else {
			errMsg := err.Error()
			if contains(errMsg, "already exists") || contains(errMsg, "address already in use") {
				addedCount++
			} else {
				logger.Warning("[SYNC] Failed to add inbound", tag)
				failedCount++
			}
		}
	}

	if addedCount > 0 {
		logger.Info("[SYNC] Added", addedCount, "inbound(s)")
	}
	if failedCount > 0 {
		logger.Warning("[SYNC] Failed to add", failedCount, "inbound(s)")
	}
}

// syncOutbounds synchronizes enabled outbounds from DB to Xray.
func (j *XrayConfigSyncJob) syncOutbounds(xrayAPI *xray.XrayAPI) {
	db := database.GetDB()
	var dbOutbounds []*model.Outbound
	err := db.Model(&model.Outbound{}).Where("enable = ?", true).Find(&dbOutbounds).Error
	if err != nil {
		logger.Warning("[SYNC] Database error:", err)
		return
	}

	if len(dbOutbounds) == 0 {
		return
	}

	// Get current outbound tags from Xray
	currentTags, err := xrayAPI.ListOutboundTags()
	if err != nil {
		logger.Warning("[SYNC] Failed to get outbound tags:", err)
		return
	}

	currentTagsMap := make(map[string]bool)
	for _, tag := range currentTags {
		currentTagsMap[tag] = true
	}

	// Build expected outbounds map
	expectedTags := make(map[string]*model.Outbound)
	for _, outbound := range dbOutbounds {
		expectedTags[outbound.Tag] = outbound
	}

	addedCount := 0
	failedCount := 0
	for tag, outbound := range expectedTags {
		if currentTagsMap[tag] {
			continue
		}

		outboundJson, err := json.MarshalIndent(outbound.GenXrayOutboundConfig(), "", "  ")
		if err != nil {
			failedCount++
			continue
		}

		err = xrayAPI.AddOutbound(outboundJson)
		if err == nil {
			addedCount++
		} else {
			errMsg := err.Error()
			if contains(errMsg, "already exists") || contains(errMsg, "existing tag found") {
				addedCount++
			} else {
				logger.Warning("[SYNC] Failed to add outbound", tag)
				failedCount++
			}
		}
	}

	if addedCount > 0 {
		logger.Info("[SYNC] Added", addedCount, "outbound(s)")
	}
	if failedCount > 0 {
		logger.Warning("[SYNC] Failed to add", failedCount, "outbound(s)")
	}
}

// contains checks if a string contains a substring (case-insensitive helper)
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		len(s) > len(substr) && (findSubstring(s, substr) >= 0))
}

func findSubstring(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
