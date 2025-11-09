package inbounds

import (
	"sort"
	"time"

	"github.com/mhsanaei/3x-ui/v2/database"
	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/util/common"
	"github.com/mhsanaei/3x-ui/v2/xray"

	"gorm.io/gorm"
)

// ProcessTrafficStats updates traffic statistics and manages client states.
// Called from CollectTraffic job every 10 seconds.
//
// Business logic (within single transaction):
// 1. addInboundTraffic - updates traffic counters for inbounds
// 2. addClientTraffic - updates traffic counters for clients
// 3. autoRenewClients - renews clients with reset > 0 (re-enables + resets traffic)
// 4. disableExceededClients - disables clients with exceeded traffic/expiry
// 5. disableExceededInbounds - disables inbounds with exceeded traffic/expiry
//
// Note: Xray API synchronization is handled by separate SyncXray job
//
// Returns: error
func (s *InboundService) ProcessTrafficStats(inboundTraffics []*xray.Traffic, clientTraffics []*xray.ClientTraffic) error {
	var err error
	db := database.GetDB()
	tx := db.Begin()

	defer func() {
		if err != nil {
			tx.Rollback()
		} else {
			tx.Commit()
		}
	}()

	err = s.addInboundTraffic(tx, inboundTraffics)
	if err != nil {
		return err
	}

	err = s.addClientTraffic(tx, clientTraffics)
	if err != nil {
		return err
	}

	count, err := s.autoRenewClients(tx)
	if err != nil {
		logger.Warning("Error in renew clients:", err)
	} else if count > 0 {
		logger.Debugf("%v clients renewed", count)
	}

	count, err = s.disableExceededClients(tx)
	if err != nil {
		logger.Warning("Error in disabling exceeded clients:", err)
	} else if count > 0 {
		logger.Debugf("%v clients disabled", count)
	}

	count, err = s.disableExceededInbounds(tx)
	if err != nil {
		logger.Warning("Error in disabling exceeded inbounds:", err)
	} else if count > 0 {
		logger.Debugf("%v inbounds disabled", count)
	}

	return nil
}

// addInboundTraffic updates traffic counters for inbounds.
//
// Business logic:
// 1. Gets current traffic (up, down) from Xray for each inbound
// 2. Adds traffic to existing DB values (increment)
// 3. Updates all_time counter (total traffic lifetime)
func (s *InboundService) addInboundTraffic(tx *gorm.DB, traffics []*xray.Traffic) error {
	if len(traffics) == 0 {
		return nil
	}

	var err error

	for _, traffic := range traffics {
		if traffic.IsInbound {
			err = tx.Model(&model.Inbound{}).Where("tag = ?", traffic.Tag).
				Updates(map[string]any{
					"up":       gorm.Expr("up + ?", traffic.Up),
					"down":     gorm.Expr("down + ?", traffic.Down),
					"all_time": gorm.Expr("COALESCE(all_time, 0) + ?", traffic.Up+traffic.Down),
				}).Error
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// addClientTraffic updates traffic counters for clients.
//
// Business logic:
// 1. Gets list of client emails from Xray statistics
// 2. Loads their records from DB (client_traffics)
// 3. Adds new traffic to current values (up, down, all_time)
// 4. Updates last_online for active clients (who have traffic > 0)
// 5. Updates global onlineClients list
func (s *InboundService) addClientTraffic(tx *gorm.DB, traffics []*xray.ClientTraffic) (err error) {
	if len(traffics) == 0 {
		onlineClients = nil
		return nil
	}

	var currentOnlineClients []string

	emails := make([]string, 0, len(traffics))
	for _, traffic := range traffics {
		emails = append(emails, traffic.Email)
	}
	dbClientTraffics := make([]*xray.ClientTraffic, 0, len(traffics))
	err = tx.Model(xray.ClientTraffic{}).Where("email IN (?)", emails).Find(&dbClientTraffics).Error
	if err != nil {
		return err
	}

	// Avoid empty slice error
	if len(dbClientTraffics) == 0 {
		return nil
	}

	dbClientTraffics, err = s.adjustTraffics(tx, dbClientTraffics)
	if err != nil {
		return err
	}

	for dbTraffic_index := range dbClientTraffics {
		for traffic_index := range traffics {
			if dbClientTraffics[dbTraffic_index].Email == traffics[traffic_index].Email {
				dbClientTraffics[dbTraffic_index].Up += traffics[traffic_index].Up
				dbClientTraffics[dbTraffic_index].Down += traffics[traffic_index].Down
				dbClientTraffics[dbTraffic_index].AllTime += (traffics[traffic_index].Up + traffics[traffic_index].Down)

				// Add user in onlineUsers array on traffic
				if traffics[traffic_index].Up+traffics[traffic_index].Down > 0 {
					currentOnlineClients = append(currentOnlineClients, traffics[traffic_index].Email)
					dbClientTraffics[dbTraffic_index].LastOnline = time.Now().UnixMilli()
				}
				break
			}
		}
	}

	onlineClients = currentOnlineClients

	err = tx.Save(dbClientTraffics).Error
	if err != nil {
		logger.Warning("AddClientTraffic update data ", err)
	}

	return nil
}

// ResetClientTrafficByEmail resets traffic counters for a specific client.
func (s *InboundService) ResetClientTrafficByEmail(clientEmail string) error {
	traffic, inbound, err := s.GetClientInboundByEmail(clientEmail)
	if err != nil {
		return err
	}
	if inbound == nil {
		return common.NewError("Inbound Not Found For Email:", clientEmail)
	}

	wasDisabled := traffic != nil && !traffic.Enable

	db := database.GetDB()
	result := db.Model(xray.ClientTraffic{}).
		Where("email = ?", clientEmail).
		Updates(map[string]any{"enable": true, "up": 0, "down": 0})

	err = result.Error
	if err != nil {
		return err
	}

	if wasDisabled && s.xrayApi != nil && traffic != nil {
		logger.Debugf("Client %s was disabled, re-adding to Xray after traffic reset", clientEmail)
		clients, err := s.GetClients(inbound)
		if err != nil {
			logger.Warning("Failed to get clients for re-enable:", err)
			return err
		}
		for _, client := range clients {
			if client.Email == clientEmail && client.Enable {
				if err := s.addClientToXray(inbound, &client); err == nil {
					logger.Info("Client re-enabled after traffic reset:", clientEmail)
				} else {
					logger.Warning("Failed to re-enable client:", clientEmail, "-", err)
				}
				break
			}
		}
	}

	return nil
}

// ResetClientTraffic resets traffic counters for a specific client (legacy method).
func (s *InboundService) ResetClientTraffic(id int, clientEmail string) (bool, error) {
	needRestart := false

	traffic, err := s.GetClientTrafficByEmail(clientEmail)
	if err != nil {
		return false, err
	}

	if !traffic.Enable {
		inbound, err := s.GetInbound(id)
		if err != nil {
			return false, err
		}
		clients, err := s.GetClients(inbound)
		if err != nil {
			return false, err
		}
		for _, client := range clients {
			if client.Email == clientEmail && client.Enable {
				if err1 := s.addClientToXray(inbound, &client); err1 == nil {
					logger.Debug("Client enabled due to reset traffic:", clientEmail)
				} else {
					logger.Debug("Error in enabling client by api:", err1)
					needRestart = true
				}
				break
			}
		}
	}

	traffic.Up = 0
	traffic.Down = 0
	traffic.Enable = true

	db := database.GetDB()
	err = db.Save(traffic).Error
	if err != nil {
		return false, err
	}

	return needRestart, nil
}

// ResetAllClientTraffics resets traffic counters for all clients in an inbound.
func (s *InboundService) ResetAllClientTraffics(id int) error {
	db := database.GetDB()
	now := time.Now().Unix() * 1000

	return db.Transaction(func(tx *gorm.DB) error {
		whereText := "inbound_id "
		if id == -1 {
			whereText += " > ?"
		} else {
			whereText += " = ?"
		}

		// Reset client traffics
		result := tx.Model(xray.ClientTraffic{}).
			Where(whereText, id).
			Updates(map[string]any{"enable": true, "up": 0, "down": 0})

		if result.Error != nil {
			return result.Error
		}

		// Update lastTrafficResetTime for the inbound(s)
		inboundWhereText := "id "
		if id == -1 {
			inboundWhereText += " > ?"
		} else {
			inboundWhereText += " = ?"
		}

		result = tx.Model(model.Inbound{}).
			Where(inboundWhereText, id).
			Update("last_traffic_reset_time", now)

		return result.Error
	})
}

// ResetAllTraffics resets traffic counters for all inbounds.
func (s *InboundService) ResetAllTraffics() error {
	db := database.GetDB()

	result := db.Model(model.Inbound{}).
		Where("user_id > ?", 0).
		Updates(map[string]any{"up": 0, "down": 0})

	err := result.Error
	return err
}

// UpdateClientTrafficByEmail updates traffic counters for a specific client.
func (s *InboundService) UpdateClientTrafficByEmail(email string, upload int64, download int64) error {
	db := database.GetDB()

	result := db.Model(xray.ClientTraffic{}).
		Where("email = ?", email).
		Updates(map[string]any{"up": upload, "down": download})

	err := result.Error
	if err != nil {
		logger.Warningf("Error updating ClientTraffic with email %s: %v", email, err)
		return err
	}
	return nil
}

// GetClientsLastOnline retrieves the last online timestamps for all clients.
func (s *InboundService) GetClientsLastOnline() (map[string]int64, error) {
	db := database.GetDB()
	var rows []xray.ClientTraffic
	err := db.Model(&xray.ClientTraffic{}).Select("email, last_online").Find(&rows).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}
	result := make(map[string]int64, len(rows))
	for _, r := range rows {
		result[r.Email] = r.LastOnline
	}
	return result, nil
}

// FilterAndSortClientEmails filters and sorts client emails by traffic usage.
func (s *InboundService) FilterAndSortClientEmails(emails []string) ([]string, []string, error) {
	db := database.GetDB()

	// Step 1: Get ClientTraffic records for emails in the input list
	var clients []xray.ClientTraffic
	err := db.Where("email IN ?", emails).Find(&clients).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, nil, err
	}

	// Step 2: Sort clients by (Up + Down) descending
	sort.Slice(clients, func(i, j int) bool {
		return (clients[i].Up + clients[i].Down) > (clients[j].Up + clients[j].Down)
	})

	// Step 3: Extract sorted valid emails and track found ones
	validEmails := make([]string, 0, len(clients))
	found := make(map[string]bool)
	for _, client := range clients {
		validEmails = append(validEmails, client.Email)
		found[client.Email] = true
	}

	// Step 4: Identify emails that were not found in the database
	extraEmails := make([]string, 0)
	for _, email := range emails {
		if !found[email] {
			extraEmails = append(extraEmails, email)
		}
	}

	return validEmails, extraEmails, nil
}
