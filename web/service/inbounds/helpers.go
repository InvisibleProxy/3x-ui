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

// checkPortExist checks if a port is already in use by another inbound.
func (s *InboundService) checkPortExist(listen string, port int, ignoreId int) (bool, error) {
	db := database.GetDB()
	if listen == "" || listen == "0.0.0.0" || listen == "::" || listen == "::0" {
		db = db.Model(model.Inbound{}).Where("port = ?", port)
	} else {
		db = db.Model(model.Inbound{}).
			Where("port = ?", port).
			Where(
				db.Model(model.Inbound{}).Where(
					"listen = ?", listen,
				).Or(
					"listen = \"\"",
				).Or(
					"listen = \"0.0.0.0\"",
				).Or(
					"listen = \"::\"",
				).Or(
					"listen = \"::0\""))
	}
	if ignoreId > 0 {
		db = db.Where("id != ?", ignoreId)
	}
	var count int64
	err := db.Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// GetAllEmails retrieves all client emails from the database.
func (s *InboundService) GetAllEmails() ([]string, error) {
	db := database.GetDB()
	var emails []string
	err := db.Raw(`
		SELECT JSON_EXTRACT(client.value, '$.email')
		FROM inbounds,
			JSON_EACH(JSON_EXTRACT(inbounds.settings, '$.clients')) AS client
		`).Scan(&emails).Error
	if err != nil {
		return nil, err
	}
	return emails, nil
}

// contains checks if a string exists in a slice (case-insensitive).
func (s *InboundService) contains(slice []string, str string) bool {
	lowerStr := strings.ToLower(str)
	for _, s := range slice {
		if strings.ToLower(s) == lowerStr {
			return true
		}
	}
	return false
}

// checkEmailsExistForClients checks if any client emails already exist in the database.
func (s *InboundService) checkEmailsExistForClients(clients []model.Client) (string, error) {
	allEmails, err := s.GetAllEmails()
	if err != nil {
		return "", err
	}
	var emails []string
	for _, client := range clients {
		if client.Email != "" {
			if s.contains(emails, client.Email) {
				return client.Email, nil
			}
			if s.contains(allEmails, client.Email) {
				return client.Email, nil
			}
			emails = append(emails, client.Email)
		}
	}
	return "", nil
}

// checkEmailExistForInbound checks if any client emails in an inbound already exist.
func (s *InboundService) checkEmailExistForInbound(inbound *model.Inbound) (string, error) {
	clients, err := s.GetClients(inbound)
	if err != nil {
		return "", err
	}
	allEmails, err := s.GetAllEmails()
	if err != nil {
		return "", err
	}
	var emails []string
	for _, client := range clients {
		if client.Email != "" {
			if s.contains(emails, client.Email) {
				return client.Email, nil
			}
			if s.contains(allEmails, client.Email) {
				return client.Email, nil
			}
			emails = append(emails, client.Email)
		}
	}
	return "", nil
}

// adjustTraffics adjusts client traffic records based on negative expiry times.
func (s *InboundService) adjustTraffics(tx *gorm.DB, dbClientTraffics []*xray.ClientTraffic) ([]*xray.ClientTraffic, error) {
	inboundIds := make([]int, 0, len(dbClientTraffics))
	for _, dbClientTraffic := range dbClientTraffics {
		if dbClientTraffic.ExpiryTime < 0 {
			inboundIds = append(inboundIds, dbClientTraffic.InboundId)
		}
	}

	if len(inboundIds) > 0 {
		var inbounds []*model.Inbound
		err := tx.Model(model.Inbound{}).Where("id IN (?)", inboundIds).Find(&inbounds).Error
		if err != nil {
			return nil, err
		}
		for inbound_index := range inbounds {
			settings := map[string]any{}
			json.Unmarshal([]byte(inbounds[inbound_index].Settings), &settings)
			clients, ok := settings["clients"].([]any)
			if ok {
				var newClients []any
				for client_index := range clients {
					c := clients[client_index].(map[string]any)
					for traffic_index := range dbClientTraffics {
						if dbClientTraffics[traffic_index].ExpiryTime < 0 && c["email"] == dbClientTraffics[traffic_index].Email {
							oldExpiryTime := c["expiryTime"].(float64)
							newExpiryTime := (time.Now().Unix() * 1000) - int64(oldExpiryTime)
							c["expiryTime"] = newExpiryTime
							c["updated_at"] = time.Now().Unix() * 1000
							dbClientTraffics[traffic_index].ExpiryTime = newExpiryTime
							break
						}
					}
					// Backfill created_at and updated_at
					if _, ok := c["created_at"]; !ok {
						c["created_at"] = time.Now().Unix() * 1000
					}
					c["updated_at"] = time.Now().Unix() * 1000
					newClients = append(newClients, any(c))
				}
				settings["clients"] = newClients
				modifiedSettings, err := json.MarshalIndent(settings, "", "  ")
				if err != nil {
					return nil, err
				}

				inbounds[inbound_index].Settings = string(modifiedSettings)
			}
		}
		err = tx.Save(inbounds).Error
		if err != nil {
			logger.Warning("AddClientTraffic update inbounds ", err)
			logger.Error(inbounds)
		}
	}

	return dbClientTraffics, nil
}

// preserveClientTimestamps preserves created_at and updated_at timestamps when updating inbound settings.
func (s *InboundService) preserveClientTimestamps(oldInbound *model.Inbound, inbound *model.Inbound) {
	var oldSettings map[string]any
	_ = json.Unmarshal([]byte(oldInbound.Settings), &oldSettings)
	emailToCreated := map[string]int64{}
	emailToUpdated := map[string]int64{}
	if oldSettings != nil {
		if oc, ok := oldSettings["clients"].([]any); ok {
			for _, it := range oc {
				if m, ok2 := it.(map[string]any); ok2 {
					if email, ok3 := m["email"].(string); ok3 {
						switch v := m["created_at"].(type) {
						case float64:
							emailToCreated[email] = int64(v)
						case int64:
							emailToCreated[email] = v
						}
						switch v := m["updated_at"].(type) {
						case float64:
							emailToUpdated[email] = int64(v)
						case int64:
							emailToUpdated[email] = v
						}
					}
				}
			}
		}
	}
	var newSettings map[string]any
	if err2 := json.Unmarshal([]byte(inbound.Settings), &newSettings); err2 == nil && newSettings != nil {
		now := time.Now().Unix() * 1000
		if nSlice, ok := newSettings["clients"].([]any); ok {
			for i := range nSlice {
				if m, ok2 := nSlice[i].(map[string]any); ok2 {
					email, _ := m["email"].(string)
					if _, ok3 := m["created_at"]; !ok3 {
						if v, ok4 := emailToCreated[email]; ok4 && v > 0 {
							m["created_at"] = v
						} else {
							m["created_at"] = now
						}
					}
					// Preserve client's updated_at if present; do not bump on parent inbound update
					if _, hasUpdated := m["updated_at"]; !hasUpdated {
						if v, ok4 := emailToUpdated[email]; ok4 && v > 0 {
							m["updated_at"] = v
						}
					}
					nSlice[i] = m
				}
			}
			newSettings["clients"] = nSlice
			if bs, err3 := json.MarshalIndent(newSettings, "", "  "); err3 == nil {
				inbound.Settings = string(bs)
			}
		}
	}
}
