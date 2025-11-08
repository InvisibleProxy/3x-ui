package service

import (
	"encoding/json"
	"errors"

	"github.com/mhsanaei/3x-ui/v2/database"
	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/xray"

	"gorm.io/gorm"
)

// OutboundService provides business logic for managing Xray outbound configurations.
// It handles outbound traffic monitoring and statistics.
type OutboundService struct {
	xrayApi *xray.XrayAPI
}

// SetXrayAPI sets the XrayAPI instance for this service.
func (s *OutboundService) SetXrayAPI(api *xray.XrayAPI) {
	s.xrayApi = api
}

func (s *OutboundService) AddTraffic(traffics []*xray.Traffic, clientTraffics []*xray.ClientTraffic) (error, bool) {
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

	err = s.addOutboundTraffic(tx, traffics)
	if err != nil {
		return err, false
	}

	return nil, false
}

func (s *OutboundService) addOutboundTraffic(tx *gorm.DB, traffics []*xray.Traffic) error {
	if len(traffics) == 0 {
		return nil
	}

	var err error

	for _, traffic := range traffics {
		if traffic.IsOutbound {
			var outbound model.Outbound
			err = tx.Model(&model.Outbound{}).Where("tag = ?", traffic.Tag).First(&outbound).Error
			if err != nil {
				if err == gorm.ErrRecordNotFound {
					logger.Debug("[TRAFFIC] Outbound not found for tag:", traffic.Tag)
					continue
				}
				return err
			}

			var outboundTraffic model.OutboundTraffics
			err = tx.Model(&model.OutboundTraffics{}).Where("outbound_id = ?", outbound.ID).
				FirstOrCreate(&outboundTraffic, model.OutboundTraffics{OutboundID: outbound.ID}).Error
			if err != nil {
				return err
			}

			outboundTraffic.Up = outboundTraffic.Up + traffic.Up
			outboundTraffic.Down = outboundTraffic.Down + traffic.Down
			outboundTraffic.Total = outboundTraffic.Up + outboundTraffic.Down

			err = tx.Save(&outboundTraffic).Error
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *OutboundService) GetOutboundsTraffic() ([]*model.OutboundTraffics, error) {
	db := database.GetDB()
	var traffics []*model.OutboundTraffics

	err := db.Model(model.OutboundTraffics{}).Preload("Outbound").Find(&traffics).Error
	if err != nil {
		logger.Warning("Failed to retrieve outbound traffics:", err)
		return nil, err
	}

	return traffics, nil
}

// GetAllOutbounds retrieves all outbounds from database.
func (s *OutboundService) GetAllOutbounds() ([]*model.Outbound, error) {
	db := database.GetDB()
	var outbounds []*model.Outbound
	err := db.Model(model.Outbound{}).Find(&outbounds).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}
	return outbounds, nil
}

// GetOutbound retrieves a single outbound by ID.
func (s *OutboundService) GetOutbound(id int) (*model.Outbound, error) {
	db := database.GetDB()
	outbound := &model.Outbound{}
	err := db.Model(model.Outbound{}).First(outbound, id).Error
	if err != nil {
		return nil, err
	}
	return outbound, nil
}

// AddOutbound creates a new outbound and adds it to Xray via gRPC.
func (s *OutboundService) AddOutbound(outbound *model.Outbound) (*model.Outbound, error) {
	db := database.GetDB()

	var count int64
	db.Model(model.Outbound{}).Where("tag = ?", outbound.Tag).Count(&count)
	if count > 0 {
		return nil, errors.New("outbound tag already exists")
	}

	err := db.Save(outbound).Error
	if err != nil {
		return nil, err
	}

	if s.xrayApi != nil && outbound.Enable {
		outboundJson, err := json.MarshalIndent(outbound.GenXrayOutboundConfig(), "", "  ")
		if err == nil {
			if err := s.xrayApi.AddOutbound(outboundJson); err != nil {
				logger.Warning("Outbound saved to DB but not applied:", outbound.Tag)
			}
		}
	}

	return outbound, nil
}

// UpdateOutbound updates an existing outbound.
func (s *OutboundService) UpdateOutbound(outbound *model.Outbound) (*model.Outbound, error) {
	db := database.GetDB()

	oldOutbound, err := s.GetOutbound(outbound.ID)
	if err != nil {
		return nil, err
	}

	oldTag := oldOutbound.Tag

	err = db.Save(outbound).Error
	if err != nil {
		return nil, err
	}

	if s.xrayApi != nil {
		s.xrayApi.DelOutbound(oldTag)

		if outbound.Enable {
			outboundJson, err := json.MarshalIndent(outbound.GenXrayOutboundConfig(), "", "  ")
			if err == nil {
				if err := s.xrayApi.AddOutbound(outboundJson); err != nil {
					logger.Warning("Outbound updated in DB but not applied:", outbound.Tag)
				}
			}
		}
	}

	return outbound, nil
}

// DelOutbound deletes an outbound by ID.
func (s *OutboundService) DelOutbound(id int) error {
	db := database.GetDB()

	outbound, err := s.GetOutbound(id)
	if err != nil {
		return err
	}

	if s.xrayApi != nil {
		s.xrayApi.DelOutbound(outbound.Tag)
	}

	return db.Delete(model.Outbound{}, id).Error
}

func (s *OutboundService) ResetOutboundTraffic(tag string) error {
	db := database.GetDB()

	if tag == "-alltags-" {
		return db.Model(model.OutboundTraffics{}).
			Updates(map[string]any{"up": 0, "down": 0, "total": 0}).Error
	}

	var outbound model.Outbound
	err := db.Model(&model.Outbound{}).Where("tag = ?", tag).First(&outbound).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			logger.Debug("[RESET TRAFFIC] Outbound not found for tag:", tag)
			return nil
		}
		return err
	}

	return db.Model(model.OutboundTraffics{}).
		Where("outbound_id = ?", outbound.ID).
		Updates(map[string]any{"up": 0, "down": 0, "total": 0}).Error
}
