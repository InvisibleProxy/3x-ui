package inbounds

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v2/database"
	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/xray"

	"gorm.io/gorm"
)

// autoRenewClients automatically renews clients with expired time.
//
// Business logic:
// 1. Finds clients where reset > 0 (auto-renewal enabled) AND time expired
// 2. Extends expiry: newExpiry = oldExpiry + (reset * 86400000 ms)
// 3. Resets traffic: up = 0, down = 0
// 4. If client was disabled (enable=false), re-enables it (enable=true)
// 5. Saves changes to DB
//
// Note: Xray API synchronization is handled by SyncXray job
//
// Returns: (number of renewed clients, error)
func (s *InboundService) autoRenewClients(tx *gorm.DB) (int64, error) {
	var traffics []*xray.ClientTraffic
	now := time.Now().Unix() * 1000
	var err error

	err = tx.Model(xray.ClientTraffic{}).Where("reset > 0 and expiry_time > 0 and expiry_time <= ?", now).Find(&traffics).Error
	if err != nil {
		return 0, err
	}
	if len(traffics) == 0 {
		return 0, nil
	}

	var inbound_ids []int
	var inbounds []*model.Inbound

	for _, traffic := range traffics {
		inbound_ids = append(inbound_ids, traffic.InboundId)
	}
	err = tx.Model(model.Inbound{}).Where("id IN ?", inbound_ids).Find(&inbounds).Error
	if err != nil {
		return 0, err
	}
	for inbound_index := range inbounds {
		settings := map[string]any{}
		json.Unmarshal([]byte(inbounds[inbound_index].Settings), &settings)
		clients := settings["clients"].([]any)
		for client_index := range clients {
			c := clients[client_index].(map[string]any)
			for traffic_index, traffic := range traffics {
				if traffic.Email == c["email"].(string) {
					newExpiryTime := traffic.ExpiryTime
					for newExpiryTime < now {
						newExpiryTime += (int64(traffic.Reset) * 86400000)
					}
					c["expiryTime"] = newExpiryTime
					traffics[traffic_index].ExpiryTime = newExpiryTime
					traffics[traffic_index].Down = 0
					traffics[traffic_index].Up = 0
					if !traffic.Enable {
						traffics[traffic_index].Enable = true
						logger.Debugf("Client %s renewed and re-enabled", traffic.Email)
					}
					clients[client_index] = any(c)
					break
				}
			}
		}
		settings["clients"] = clients
		newSettings, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			return 0, err
		}
		inbounds[inbound_index].Settings = string(newSettings)
	}
	err = tx.Save(inbounds).Error
	if err != nil {
		return 0, err
	}
	err = tx.Save(traffics).Error
	if err != nil {
		return 0, err
	}

	return int64(len(traffics)), nil
}

// disableExceededInbounds disables inbounds with exceeded limits.
//
// Business logic:
// 1. Finds inbounds where enable=true AND (traffic exceeded OR time expired)
// 2. Sets enable=false in DB for all found inbounds
//
// Note: Xray API synchronization (removing inbounds) is handled by SyncXray job
//
// Returns: (number of disabled inbounds, error)
func (s *InboundService) disableExceededInbounds(tx *gorm.DB) (int64, error) {
	now := time.Now().Unix() * 1000

	result := tx.Model(model.Inbound{}).
		Where("((total > 0 and up + down >= total) or (expiry_time > 0 and expiry_time <= ?)) and enable = ?", now, true).
		Update("enable", false)

	count := result.RowsAffected
	if count > 0 {
		logger.Debugf("Disabled %d inbound(s) with exceeded limits in DB", count)
	}

	return count, result.Error
}

// disableExceededClients disables clients with exceeded limits.
//
// Business logic:
// 1. Finds clients where enable=true AND (traffic exceeded OR time expired)
// 2. Sets enable=false in DB for all found clients
//
// Note: Xray API synchronization (removing clients) is handled by SyncXray job
//
// Returns: (number of disabled clients, error)
func (s *InboundService) disableExceededClients(tx *gorm.DB) (int64, error) {
	now := time.Now().Unix() * 1000

	result := tx.Model(xray.ClientTraffic{}).
		Where("((total > 0 and up + down >= total) or (expiry_time > 0 and expiry_time <= ?)) and enable = ?", now, true).
		Update("enable", false)

	count := result.RowsAffected
	if count > 0 {
		logger.Debugf("Disabled %d client(s) with exceeded limits in DB", count)
	}

	return count, result.Error
}

// DelDepletedClients deletes clients with depleted resources (expired or traffic exhausted).
func (s *InboundService) DelDepletedClients(id int) (err error) {
	db := database.GetDB()
	tx := db.Begin()
	defer func() {
		if err == nil {
			tx.Commit()
		} else {
			tx.Rollback()
		}
	}()

	whereText := "reset = 0 and inbound_id "
	if id < 0 {
		whereText += "> ?"
	} else {
		whereText += "= ?"
	}

	// Only consider truly depleted clients: expired OR traffic exhausted
	now := time.Now().Unix() * 1000
	depletedClients := []xray.ClientTraffic{}
	err = db.Model(xray.ClientTraffic{}).
		Where(whereText+" and ((total > 0 and up + down >= total) or (expiry_time > 0 and expiry_time <= ?))", id, now).
		Select("inbound_id, GROUP_CONCAT(email) as email").
		Group("inbound_id").
		Find(&depletedClients).Error
	if err != nil {
		return err
	}

	for _, depletedClient := range depletedClients {
		emails := strings.Split(depletedClient.Email, ",")
		oldInbound, err := s.GetInbound(depletedClient.InboundId)
		if err != nil {
			return err
		}
		var oldSettings map[string]any
		err = json.Unmarshal([]byte(oldInbound.Settings), &oldSettings)
		if err != nil {
			return err
		}

		oldClients := oldSettings["clients"].([]any)
		var newClients []any
		for _, client := range oldClients {
			deplete := false
			c := client.(map[string]any)
			for _, email := range emails {
				if email == c["email"].(string) {
					deplete = true
					break
				}
			}
			if !deplete {
				newClients = append(newClients, client)
			}
		}
		if len(newClients) > 0 {
			oldSettings["clients"] = newClients

			newSettings, err := json.MarshalIndent(oldSettings, "", "  ")
			if err != nil {
				return err
			}

			oldInbound.Settings = string(newSettings)
			err = tx.Save(oldInbound).Error
			if err != nil {
				return err
			}
		} else {
			// Delete inbound if no client remains
			s.DelInbound(depletedClient.InboundId)
		}
	}

	// Delete stats only for truly depleted clients
	err = tx.Where(whereText+" and ((total > 0 and up + down >= total) or (expiry_time > 0 and expiry_time <= ?))", id, now).Delete(xray.ClientTraffic{}).Error
	if err != nil {
		return err
	}

	return nil
}
