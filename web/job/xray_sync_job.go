package job

import (
	"encoding/json"
	"fmt"

	"github.com/mhsanaei/3x-ui/v2/database"
	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/web/service"
	"github.com/mhsanaei/3x-ui/v2/xray"
)

// SyncXray periodically synchronizes Xray configuration from database.
// This ensures Xray always has up-to-date inbound configurations.
type SyncXray struct {
	xrayService *service.XrayService
}

// NewSyncXray creates a new sync job instance.
func NewSyncXray(xrayService *service.XrayService) *SyncXray {
	return &SyncXray{
		xrayService: xrayService,
	}
}

// Run performs the configuration synchronization.
func (j *SyncXray) Run() {
	logger.Debug("[SYNC] Running SyncXray job")

	settingService := service.SettingService{}
	xrayEnabled, err := settingService.GetXrayEnabled()
	if err != nil {
		logger.Debug("[SYNC] Cannot read xrayEnabled, skipping")
		return
	}

	if !xrayEnabled {
		logger.Debug("[SYNC] Xray is disabled, skipping sync")
		return
	}

	xrayAPI := j.xrayService.GetXrayAPI()
	if xrayAPI == nil {
		logger.Debug("[SYNC] XrayAPI is nil, skipping sync")
		return
	}

	j.syncInbounds(xrayAPI)
	j.syncInboundClients(xrayAPI)
	j.syncOutbounds(xrayAPI)

	logger.Debug("[SYNC] SyncXray job completed")
}

// syncInbounds synchronizes enabled inbounds from DB to Xray.
// Called from SyncXray job every 30 seconds to restore missing inbounds.
//
// Business logic:
// 1. Loads all enabled inbounds from DB (with ClientStats)
// 2. Gets list of current inbound tags from Xray via API
// 3. Finds inbounds that exist in DB but missing in Xray
// 4. For each missing inbound:
//   - Filters clients (removes disabled and with exceeded traffic)
//   - Generates JSON configuration for Xray
//   - Adds inbound to Xray via AddInbound API
//
// 5. Logs statistics (added/failed count)
//
// Note: Ignores "already exists" and "address already in use" errors
func (j *SyncXray) syncInbounds(xrayAPI *xray.XrayAPI) {
	db := database.GetDB()
	var dbInbounds []*model.Inbound
	err := db.Model(&model.Inbound{}).Preload("ClientStats").Where("enable = ?", true).Find(&dbInbounds).Error
	if err != nil {
		logger.Warning("[SYNC] Database error:", err)
		return
	}

	if len(dbInbounds) == 0 {
		logger.Debug("[SYNC] No enabled inbounds found in DB")
		return
	}

	currentTags, err := xrayAPI.ListInboundTags()
	if err != nil {
		logger.Warning("[SYNC] Failed to list inbound tags:", err)
		return
	}

	currentTagsMap := make(map[string]bool)
	for _, tag := range currentTags {
		currentTagsMap[tag] = true
	}

	addedCount := 0
	failedCount := 0

	for _, inbound := range dbInbounds {
		if currentTagsMap[inbound.Tag] {
			continue
		}

		filteredInbound := j.filterClientsForInbound(inbound)
		inboundJson, err := json.MarshalIndent(filteredInbound.GenXrayInboundConfig(), "", "  ")
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
				logger.Debugf("[SYNC] Inbound %s already exists in Xray (might need update instead of add)", inbound.Tag)
				addedCount++
			} else {
				logger.Warning("[SYNC] Failed to add inbound", inbound.Tag)
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
	if addedCount == 0 && failedCount == 0 {
		logger.Debug("[SYNC] All inbounds already exist in Xray")
	}
}

// filterClientsForInbound filters clients in inbound settings before adding to Xray.
// Called from syncInbounds for each inbound that needs to be added to Xray.
//
// Business logic:
// 1. Parses Settings JSON field from inbound
// 2. Extracts clients array from settings["clients"]
// 3. For each client checks ClientStats from DB:
//   - Removes client if enable=false
//   - Removes client if traffic exceeded (up+down >= total)
//
// 4. Updates settings["clients"] with filtered list
// 5. Serializes back to JSON and writes to inbound.Settings
//
// Returns: modified inbound with filtered clients
func (j *SyncXray) filterClientsForInbound(inbound *model.Inbound) *model.Inbound {
	settings := make(map[string]any)
	if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
		logger.Warning("[SYNC] Failed to parse inbound settings:", err)
		return inbound
	}

	clients, ok := settings["clients"].([]any)
	if !ok {
		return inbound
	}

	clientStats := inbound.ClientStats
	logger.Debugf("[SYNC] Filtering clients for inbound %s: %d clients in settings, %d in stats", inbound.Tag, len(clients), len(clientStats))

	removedCount := 0
	for i := len(clients) - 1; i >= 0; i-- {
		client := clients[i]
		c, ok := client.(map[string]any)
		if !ok {
			continue
		}

		email, ok := c["email"].(string)
		if !ok {
			continue
		}

		for _, clientTraffic := range clientStats {
			if clientTraffic.Email == email {
				trafficExceeded := clientTraffic.Total > 0 && (clientTraffic.Up+clientTraffic.Down) >= clientTraffic.Total

				logger.Debugf("[SYNC] Client %s: enable=%v, up+down=%d, total=%d, exceeded=%v",
					email, clientTraffic.Enable, clientTraffic.Up+clientTraffic.Down, clientTraffic.Total, trafficExceeded)

				if !clientTraffic.Enable || trafficExceeded {
					logger.Debugf("[SYNC] Removing client %s from inbound config", email)
					clients = append(clients[:i], clients[i+1:]...)
					removedCount++
				}
				break
			}
		}
	}

	if removedCount > 0 {
		logger.Debugf("[SYNC] Removed %d client(s) from inbound %s config", removedCount, inbound.Tag)
	}

	settings["clients"] = clients
	if modifiedSettings, err := json.MarshalIndent(settings, "", "  "); err == nil {
		inbound.Settings = string(modifiedSettings)
	}

	return inbound
}

// syncInboundClients synchronizes clients for existing inbounds.
// Called from SyncXray job every 30 seconds for bidirectional client synchronization.
//
// Business logic:
// 1. Loads all enabled inbounds from DB (with ClientStats)
// 2. Gets ACTUAL client list from Xray via GetInboundUsers for each inbound
// 3. For each inbound that exists in Xray:
//   - Determines which clients should be active (enable=true and traffic not exceeded)
//   - ADDS missing active clients via AddUser API
//   - REMOVES present inactive clients via RemoveUser API
//
// 4. Logs statistics (added/removed/failed count)
//
// Note:
// - Uses GetInboundUsers instead of GetTraffic to get accurate user list
// - GetTraffic returns only statistics (may be outdated if client was removed)
// - Ignores "not found" error when removing (client already removed)
func (j *SyncXray) syncInboundClients(xrayAPI *xray.XrayAPI) {
	db := database.GetDB()
	var dbInbounds []*model.Inbound
	err := db.Model(&model.Inbound{}).Preload("ClientStats").Where("enable = ?", true).Find(&dbInbounds).Error
	if err != nil {
		logger.Warning("[SYNC] Database error:", err)
		return
	}

	if len(dbInbounds) == 0 {
		logger.Debug("[SYNC] No enabled inbounds found for client sync")
		return
	}

	currentTags, err := xrayAPI.ListInboundTags()
	if err != nil {
		logger.Warning("[SYNC] Failed to list inbound tags for client sync:", err)
		return
	}

	currentTagsMap := make(map[string]bool)
	for _, tag := range currentTags {
		currentTagsMap[tag] = true
	}

	xrayClientsMap := make(map[string]map[string]bool)

	for _, tag := range currentTags {
		if tag == "api" || tag == "metrics_out" {
			continue
		}

		users, err := xrayAPI.GetInboundUsers(tag)
		if err != nil {
			logger.Warningf("[SYNC] Failed to get users for inbound %s: %v", tag, err)
			continue
		}

		userMap := make(map[string]bool)
		for _, email := range users {
			userMap[email] = true
		}
		xrayClientsMap[tag] = userMap

		logger.Debugf("[SYNC] Inbound %s has %d users in Xray", tag, len(userMap))
	}

	addedCount := 0
	removedCount := 0
	failedCount := 0

	for _, inbound := range dbInbounds {
		if !currentTagsMap[inbound.Tag] {
			continue
		}

		settings := make(map[string]any)
		if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
			continue
		}

		clients, ok := settings["clients"].([]any)
		if !ok {
			continue
		}

		logger.Debugf("[SYNC] Inbound %s: found %d clients in settings, %d in ClientStats",
			inbound.Tag, len(clients), len(inbound.ClientStats))

		for _, client := range clients {
			c, ok := client.(map[string]any)
			if !ok {
				continue
			}

			email, ok := c["email"].(string)
			if !ok {
				continue
			}

			var clientTraffic *xray.ClientTraffic
			for i := range inbound.ClientStats {
				if inbound.ClientStats[i].Email == email {
					clientTraffic = &inbound.ClientStats[i]
					break
				}
			}

			if clientTraffic == nil {
				logger.Debugf("[SYNC] Client %s not found in ClientStats, skipping", email)
				continue
			}

			inXray := xrayClientsMap[inbound.Tag] != nil && xrayClientsMap[inbound.Tag][email]
			logger.Debugf("[SYNC] Processing client %s: enable=%v, traffic=%d/%d, inXray=%v",
				email, clientTraffic.Enable, clientTraffic.Up+clientTraffic.Down, clientTraffic.Total, inXray)

			trafficExceeded := clientTraffic.Total > 0 && (clientTraffic.Up+clientTraffic.Down) >= clientTraffic.Total
			shouldBeActive := clientTraffic.Enable && !trafficExceeded

			if shouldBeActive && !inXray {
				logger.Debugf("[SYNC] Client %s not found in Xray (enable=%v, traffic=%d/%d), attempting to add",
					email, clientTraffic.Enable, clientTraffic.Up+clientTraffic.Down, clientTraffic.Total)

				err := xrayAPI.AddUser(string(inbound.Protocol), inbound.Tag, c)
				if err != nil {
					logger.Warningf("[SYNC] Failed to add client %s to inbound %s: %v", email, inbound.Tag, err)
					failedCount++
				} else {
					logger.Debugf("[SYNC] Successfully added client %s to inbound %s", email, inbound.Tag)
					if xrayClientsMap[inbound.Tag] == nil {
						xrayClientsMap[inbound.Tag] = make(map[string]bool)
					}
					xrayClientsMap[inbound.Tag][email] = true
					addedCount++
				}
			}

			if !shouldBeActive && inXray {
				reason := ""
				if !clientTraffic.Enable {
					reason = "disabled"
				} else if trafficExceeded {
					reason = fmt.Sprintf("traffic exceeded (%d/%d)", clientTraffic.Up+clientTraffic.Down, clientTraffic.Total)
				}

				logger.Debugf("[SYNC] Removing client %s from inbound %s (reason: %s)", email, inbound.Tag, reason)

				err := xrayAPI.RemoveUser(inbound.Tag, email)
				if err != nil {
					if contains(err.Error(), "not found") {
						logger.Debugf("[SYNC] Client %s already removed from Xray", email)
						if xrayClientsMap[inbound.Tag] != nil {
							delete(xrayClientsMap[inbound.Tag], email)
						}
					} else {
						logger.Warningf("[SYNC] Failed to remove client %s from inbound %s: %v", email, inbound.Tag, err)
						failedCount++
					}
				} else {
					logger.Debugf("[SYNC] Successfully removed client %s from inbound %s", email, inbound.Tag)
					if xrayClientsMap[inbound.Tag] != nil {
						delete(xrayClientsMap[inbound.Tag], email)
					}
					removedCount++
				}
			}
		}
	}

	if addedCount > 0 {
		logger.Infof("[SYNC] Added %d missing client(s) to existing inbounds", addedCount)
	}
	if removedCount > 0 {
		logger.Infof("[SYNC] Removed %d disabled/exceeded client(s) from Xray", removedCount)
	}
	if failedCount > 0 {
		logger.Warningf("[SYNC] Failed to sync %d client(s)", failedCount)
	}
}

// syncOutbounds synchronizes enabled outbounds from DB to Xray.
// Called from SyncXray job every 30 seconds to restore missing outbounds.
//
// Business logic:
// 1. Loads all enabled outbounds from DB
// 2. Gets list of current outbound tags from Xray via API
// 3. Finds outbounds that exist in DB but missing in Xray
// 4. For each missing outbound:
//   - Generates JSON configuration for Xray
//   - Adds outbound to Xray via AddOutbound API
//
// 5. Logs statistics (added/failed count)
//
// Note: Ignores "already exists" and "existing tag found" errors
func (j *SyncXray) syncOutbounds(xrayAPI *xray.XrayAPI) {
	db := database.GetDB()
	var dbOutbounds []*model.Outbound
	err := db.Model(&model.Outbound{}).Where("enable = ?", true).Find(&dbOutbounds).Error
	if err != nil {
		logger.Warning("[SYNC] Database error:", err)
		return
	}

	if len(dbOutbounds) == 0 {
		logger.Debug("[SYNC] No enabled outbounds found in DB")
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
				logger.Debugf("[SYNC] Outbound %s already exists in Xray (might need update instead of add)", tag)
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
