package job

import (
	"encoding/json"

	"github.com/mhsanaei/3x-ui/v2/database"
	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/web/service"
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

	// Get all enabled inbounds from database
	db := database.GetDB()
	var dbInbounds []*model.Inbound
	err = db.Model(&model.Inbound{}).Where("enable = ?", true).Find(&dbInbounds).Error
	if err != nil {
		logger.Warning("[SYNC] Database error:", err)
		return
	}

	if len(dbInbounds) == 0 {
		return
	}

	// Get current inbound tags from running Xray
	currentTags, err := xrayAPI.ListInboundTags()
	if err != nil {
		return
	}

	// Build maps for comparison
	currentTagsMap := make(map[string]bool)
	for _, tag := range currentTags {
		currentTagsMap[tag] = true
	}

	expectedTags := make(map[string]*model.Inbound)
	for _, inbound := range dbInbounds {
		expectedTags[inbound.Tag] = inbound
	}

	// Check which inbounds need to be added
	addedCount := 0
	failedCount := 0

	for tag, inbound := range expectedTags {
		// Check if inbound already exists in Xray
		if currentTagsMap[tag] {
			continue
		}

		// Inbound is missing - add it
		inboundJson, err := json.MarshalIndent(inbound.GenXrayInboundConfig(), "", "  ")
		if err != nil {
			failedCount++
			continue
		}

		err = xrayAPI.AddInbound(inboundJson)
		if err == nil {
			addedCount++
		} else {
			// Check if error is "already exists" (race condition)
			errMsg := err.Error()
			if contains(errMsg, "already exists") || contains(errMsg, "address already in use") {
				addedCount++
			} else {
				logger.Warning("[SYNC] Failed to add", tag)
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
