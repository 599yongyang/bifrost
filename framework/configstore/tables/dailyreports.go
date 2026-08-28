package tables

import (
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
)

const DefaultDailyReportSettingsID = "default"

// TableDailyReportSettings stores the singleton daily-report configuration.
// The selected notification channels are existing alert channel IDs so report
// delivery can reuse the alerting transport implementations and secret storage.
type TableDailyReportSettings struct {
	ID                     string    `gorm:"type:varchar(32);primaryKey" json:"id"`
	Enabled                bool      `gorm:"not null;default:false" json:"enabled"`
	Timezone               string    `gorm:"type:varchar(128);not null" json:"timezone"`
	GenerateTime           string    `gorm:"type:varchar(5);not null;default:'03:00'" json:"generate_time"`
	SendTime               string    `gorm:"type:varchar(5);not null" json:"send_time"`
	SlowThresholdMs        int64     `gorm:"not null;default:10000" json:"slow_threshold_ms"`
	InternalEnabled        bool      `gorm:"not null;default:true" json:"internal_enabled"`
	ExternalEnabled        bool      `gorm:"not null;default:false" json:"external_enabled"`
	InternalChannelIDsJSON string    `gorm:"type:text;not null" json:"-"`
	ExternalChannelIDsJSON string    `gorm:"type:text;not null" json:"-"`
	CreatedAt              time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt              time.Time `gorm:"index;not null" json:"updated_at"`

	InternalChannelIDs []string `gorm:"-" json:"internal_channel_ids"`
	ExternalChannelIDs []string `gorm:"-" json:"external_channel_ids"`
}

func (TableDailyReportSettings) TableName() string { return "daily_report_settings" }

func (s *TableDailyReportSettings) BeforeSave(*gorm.DB) error {
	if err := s.Validate(); err != nil {
		return err
	}
	internal, err := json.Marshal(s.InternalChannelIDs)
	if err != nil {
		return err
	}
	external, err := json.Marshal(s.ExternalChannelIDs)
	if err != nil {
		return err
	}
	s.InternalChannelIDsJSON = string(internal)
	s.ExternalChannelIDsJSON = string(external)
	return nil
}

func (s *TableDailyReportSettings) AfterFind(*gorm.DB) error {
	if s.InternalChannelIDsJSON == "" {
		s.InternalChannelIDs = []string{}
	} else if err := json.Unmarshal([]byte(s.InternalChannelIDsJSON), &s.InternalChannelIDs); err != nil {
		return err
	}
	if s.ExternalChannelIDsJSON == "" {
		s.ExternalChannelIDs = []string{}
		return nil
	}
	return json.Unmarshal([]byte(s.ExternalChannelIDsJSON), &s.ExternalChannelIDs)
}

func (s *TableDailyReportSettings) Validate() error {
	if s == nil {
		return fmt.Errorf("daily report settings cannot be nil")
	}
	if s.ID == "" {
		s.ID = DefaultDailyReportSettingsID
	}
	if s.Timezone == "" {
		return fmt.Errorf("timezone is required")
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return fmt.Errorf("invalid timezone %q", s.Timezone)
	}
	generateHour, generateMinute, err := parseDailyReportClockTime("generate_time", s.GenerateTime)
	if err != nil {
		return err
	}
	sendHour, sendMinute, err := parseDailyReportClockTime("send_time", s.SendTime)
	if err != nil {
		return err
	}
	if generateHour*60+generateMinute >= sendHour*60+sendMinute {
		return fmt.Errorf("generate_time must be earlier than send_time")
	}
	if s.SlowThresholdMs < 0 {
		return fmt.Errorf("slow_threshold_ms must be zero or greater")
	}
	return nil
}

func (s *TableDailyReportSettings) SendHourMinute() (int, int, error) {
	return parseDailyReportClockTime("send_time", s.SendTime)
}

func (s *TableDailyReportSettings) GenerateHourMinute() (int, int, error) {
	return parseDailyReportClockTime("generate_time", s.GenerateTime)
}

func parseDailyReportClockTime(field, value string) (int, int, error) {
	parsed, err := time.Parse("15:04", value)
	if err != nil || parsed.Format("15:04") != value {
		return 0, 0, fmt.Errorf("%s must use HH:MM", field)
	}
	return parsed.Hour(), parsed.Minute(), nil
}
