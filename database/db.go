// Package database provides database initialization, migration, and management utilities
// for the 3x-ui panel using GORM with SQLite.
package database

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/mhsanaei/3x-ui/v2/config"
	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/util/crypto"
	"github.com/mhsanaei/3x-ui/v2/xray"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

var db *gorm.DB

const (
	defaultUsername = "admin"
	defaultPassword = "admin"
)

func initModels() error {
	models := []any{
		&model.User{},
		&model.Inbound{},
		&model.Outbound{},
		&model.OutboundTraffics{},
		&model.Setting{},
		&model.InboundClientIps{},
		&xray.ClientTraffic{},
		&model.HistoryOfSeeders{},
	}
	for _, model := range models {
		if err := db.AutoMigrate(model); err != nil {
			log.Printf("Error auto migrating model: %v", err)
			return err
		}
	}
	return nil
}

func SetJSON(db *gorm.DB, key string, raw []byte) error {
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&model.Setting{
		Key:   key,
		Value: string(raw),
	}).Error
}

func GetJSON(db *gorm.DB, key string) ([]byte, error) {
	var s model.Setting
	if err := db.Where("key = ?", key).First(&s).Error; err != nil {
		return nil, err
	}
	return []byte(s.Value), nil
}

// initUser creates a default admin user if the users table is empty.
func initUser() error {
	empty, err := isTableEmpty("users")
	if err != nil {
		log.Printf("Error checking if users table is empty: %v", err)
		return err
	}
	if empty {
		hashedPassword, err := crypto.HashPasswordAsBcrypt(defaultPassword)

		if err != nil {
			log.Printf("Error hashing default password: %v", err)
			return err
		}

		user := &model.User{
			Username: defaultUsername,
			Password: hashedPassword,
		}
		return db.Create(user).Error
	}
	return nil
}

// runSeeders migrates user passwords to bcrypt and records seeder execution to prevent re-running.
func runSeeders(isUsersEmpty bool) error {
	empty, err := isTableEmpty("history_of_seeders")
	if err != nil {
		log.Printf("Error checking if users table is empty: %v", err)
		return err
	}

	if empty && isUsersEmpty {
		hashSeeder := &model.HistoryOfSeeders{
			SeederName: "UserPasswordHash",
		}
		if err := db.Create(hashSeeder).Error; err != nil {
			return err
		}
	} else {
		var seedersHistory []string
		db.Model(&model.HistoryOfSeeders{}).Pluck("seeder_name", &seedersHistory)

		if !slices.Contains(seedersHistory, "UserPasswordHash") && !isUsersEmpty {
			var users []model.User
			db.Find(&users)

			for _, user := range users {
				hashedPassword, err := crypto.HashPasswordAsBcrypt(user.Password)
				if err != nil {
					log.Printf("Error hashing password for user '%s': %v", user.Username, err)
					return err
				}
				db.Model(&user).Update("password", hashedPassword)
			}

			hashSeeder := &model.HistoryOfSeeders{
				SeederName: "UserPasswordHash",
			}
			if err := db.Create(hashSeeder).Error; err != nil {
				return err
			}
		}
	}

	// Run xray config initialization seeder
	var seedersHistory []string
	db.Model(&model.HistoryOfSeeders{}).Pluck("seeder_name", &seedersHistory)

	if !slices.Contains(seedersHistory, "XrayConfigInit") {
		if err := seedXrayConfigFromFile(); err != nil {
			log.Printf("Error seeding xray config: %v", err)
			return err
		}
	}

	return nil
}

// seedXrayConfigFromFile reads xray.config.json once and initializes DB.
func seedXrayConfigFromFile() error {
	configPath := xray.GetConfigPath()
	configData, err := os.ReadFile(configPath)

	if err != nil {
		log.Printf("Warning: Could not read xray config from %s: %v", configPath, err)
		var existing model.HistoryOfSeeders
		if err := db.Where("seeder_name = ?", "XrayConfigInit").First(&existing).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				seeder := &model.HistoryOfSeeders{SeederName: "XrayConfigInit"}
				return db.Create(seeder).Error
			}
			return err
		}
		return nil
	}

	var configMap map[string]any
	if err := json.Unmarshal(configData, &configMap); err != nil {
		log.Printf("Error parsing xray.config.json: %v", err)
		return err
	}

	saveSection := func(key string) {
		if v, ok := configMap[strings.TrimPrefix(key, "xray.")]; ok {
			if v == nil {
				return
			}
			raw, _ := json.Marshal(v)
			if len(raw) == 0 || string(raw) == "null" {
				return
			}
			if err := SetJSON(db, key, raw); err != nil {
				log.Printf("Error saving section %s: %v", key, err)
			}
		}
	}

	saveSection(model.KeyLog)
	saveSection(model.KeyAPI)
	saveSection(model.KeyPolicy)
	saveSection(model.KeyRouting)
	saveSection(model.KeyStats)
	saveSection(model.KeyMetrics)
	saveSection(model.KeyTransport)
	saveSection(model.KeyDNS)
	saveSection(model.KeyReverse)
	saveSection(model.KeyFakeDNS)
	saveSection(model.KeyObservatory)
	saveSection(model.KeyBurstObserv)

	apiInbounds := []any{}
	if inboundsAny, ok := configMap["inbounds"].([]any); ok {
		for _, inbound := range inboundsAny {
			if m, ok := inbound.(map[string]any); ok && m["tag"] == "api" {
				apiInbounds = append(apiInbounds, inbound)
			}
		}
	}

	templateConfig := map[string]any{
		"inbounds":  apiInbounds,
		"outbounds": []any{},
	}

	templateBytes, _ := json.MarshalIndent(templateConfig, "", "  ")
	if err := SetJSON(db, model.KeyXrayTemplate, templateBytes); err != nil {
		log.Printf("Error saving xray template: %v", err)
	}

	if outboundsAny, ok := configMap["outbounds"].([]any); ok {
		for _, outboundAny := range outboundsAny {
			outboundMap, ok := outboundAny.(map[string]any)
			if !ok {
				continue
			}
			tag, _ := outboundMap["tag"].(string)
			protocol, _ := outboundMap["protocol"].(string)
			if tag == "" || protocol == "" {
				continue
			}
			var existing model.Outbound
			err := db.Where("tag = ?", tag).First(&existing).Error
			if err != nil && err != gorm.ErrRecordNotFound {
				return err
			}
			if err != gorm.ErrRecordNotFound {
				log.Printf("Outbound '%s' already exists, skipping", tag)
				continue
			}
			settingsBytes, _ := json.Marshal(outboundMap["settings"])
			streamSettingsBytes, _ := json.Marshal(outboundMap["streamSettings"])
			muxBytes, _ := json.Marshal(outboundMap["mux"])
			proxySettingsBytes, _ := json.Marshal(outboundMap["proxySettings"])

			outbound := model.Outbound{
				Tag:            tag,
				Protocol:       protocol,
				Settings:       string(settingsBytes),
				StreamSettings: string(streamSettingsBytes),
				Mux:            string(muxBytes),
				ProxySettings:  string(proxySettingsBytes),
				Remark:         fmt.Sprintf("Default %s outbound", tag),
				Enable:         true,
			}
			if err := db.Create(&outbound).Error; err != nil {
				log.Printf("Error creating outbound '%s': %v", tag, err)
				return err
			}
			log.Printf("Created outbound from config: %s", tag)
		}
	}

	var existing model.HistoryOfSeeders
	if err := db.Where("seeder_name = ?", "XrayConfigInit").First(&existing).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			seeder := &model.HistoryOfSeeders{SeederName: "XrayConfigInit"}
			return db.Create(seeder).Error
		}
		return err
	}
	return nil // Seeder already exists
}

// isTableEmpty returns true if the named table contains zero rows.
func isTableEmpty(tableName string) (bool, error) {
	var count int64
	err := db.Table(tableName).Count(&count).Error
	return count == 0, err
}

// InitDB sets up the database connection, migrates models, and runs seeders.
func InitDB(dbPath string) error {
	if err := os.MkdirAll(path.Dir(dbPath), fs.ModePerm); err != nil {
		return err
	}

	var gormLogger logger.Interface
	if config.IsDebug() {
		gormLogger = logger.Default.LogMode(logger.Warn)
	} else {
		gormLogger = logger.Discard
	}

	// Enable foreign keys for all connections via DSN
	dsn := dbPath + "?_foreign_keys=on&_busy_timeout=5000"
	var err error
	db, err = gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormLogger})
	if err != nil {
		return err
	}

	// Verify foreign keys are enabled
	var fk int
	if err := db.Raw("PRAGMA foreign_keys").Scan(&fk).Error; err == nil && fk != 1 && config.IsDebug() {
		log.Printf("WARNING: SQLite foreign_keys=OFF - CASCADE will not work")
	}

	if err := initModels(); err != nil {
		return err
	}

	isUsersEmpty, err := isTableEmpty("users")
	if err != nil {
		return err
	}

	if err := initUser(); err != nil {
		return err
	}
	return runSeeders(isUsersEmpty)
}

// CloseDB closes the database connection if it exists.
func CloseDB() error {
	if db != nil {
		sqlDB, err := db.DB()
		if err != nil {
			return err
		}
		return sqlDB.Close()
	}
	return nil
}

// GetDB returns the global GORM database instance.
func GetDB() *gorm.DB {
	return db
}

// IsNotFound checks if the given error is a GORM record not found error.
func IsNotFound(err error) bool {
	return err == gorm.ErrRecordNotFound
}

// IsSQLiteDB checks if the given file is a valid SQLite database by reading its signature.
func IsSQLiteDB(file io.ReaderAt) (bool, error) {
	signature := []byte("SQLite format 3\x00")
	buf := make([]byte, len(signature))
	_, err := file.ReadAt(buf, 0)
	if err != nil {
		return false, err
	}
	return bytes.Equal(buf, signature), nil
}

// Checkpoint performs a WAL checkpoint on the SQLite database to ensure data consistency.
func Checkpoint() error {
	// Update WAL
	err := db.Exec("PRAGMA wal_checkpoint;").Error
	if err != nil {
		return err
	}
	return nil
}

// ValidateSQLiteDB opens the provided sqlite DB path with a throw-away connection
// and runs a PRAGMA integrity_check to ensure the file is structurally sound.
// It does not mutate global state or run migrations.
func ValidateSQLiteDB(dbPath string) error {
	if _, err := os.Stat(dbPath); err != nil { // file must exist
		return err
	}
	gdb, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return err
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	var res string
	if err := gdb.Raw("PRAGMA integrity_check;").Scan(&res).Error; err != nil {
		return err
	}
	if res != "ok" {
		return errors.New("sqlite integrity check failed: " + res)
	}
	return nil
}
