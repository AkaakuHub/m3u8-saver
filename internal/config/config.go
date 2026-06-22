package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

const dateLayout = "20060102"

type Config struct {
	BaseURL             string         `json:"baseUrl"`
	ArchiveJSONPath     string         `json:"archiveJsonPath"`
	StartDate           string         `json:"startDate"`
	EndDate             string         `json:"endDate"`
	OutDir              string         `json:"outDir"`
	DryRun              bool           `json:"dryRun"`
	DownloadAllVariants bool           `json:"downloadAllVariants"`
	RetryCount          int            `json:"retryCount"`
	Parallelism         int            `json:"parallelism"`
	RequestTimeoutSec   int            `json:"requestTimeoutSec"`
	Discord             *DiscordConfig `json:"discord"`
}

type DiscordConfig struct {
	WebhookURL  string `json:"webhookUrl"`
	NotifyEvery int    `json:"notifyEvery"`
}

func Load(path string) (Config, error) {
	var cfg Config

	body, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("failed to read config file: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("failed to parse config json: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) Validate() error {
	if c.BaseURL == "" {
		return fmt.Errorf("baseUrl is required")
	}
	if !strings.HasPrefix(c.BaseURL, "http://") && !strings.HasPrefix(c.BaseURL, "https://") {
		return fmt.Errorf("baseUrl must be an absolute http(s) URL")
	}
	if c.OutDir == "" {
		return fmt.Errorf("outDir is required")
	}
	if _, err := time.Parse(dateLayout, c.StartDate); err != nil {
		return fmt.Errorf("startDate must be in YYYYMMDD format: %w", err)
	}
	if _, err := time.Parse(dateLayout, c.EndDate); err != nil {
		return fmt.Errorf("endDate must be in YYYYMMDD format: %w", err)
	}
	if c.StartDate > c.EndDate {
		return fmt.Errorf("startDate must be less than or equal to endDate")
	}
	if c.RetryCount < 0 {
		return fmt.Errorf("retryCount must be 0 or greater")
	}
	if c.Parallelism <= 0 {
		return fmt.Errorf("parallelism must be greater than 0")
	}
	if c.RequestTimeoutSec <= 0 {
		return fmt.Errorf("requestTimeoutSec must be greater than 0")
	}
	if c.Discord != nil {
		if c.Discord.WebhookURL == "" {
			return fmt.Errorf("discord.webhookUrl is required when discord is enabled")
		}
		if c.Discord.NotifyEvery <= 0 {
			return fmt.Errorf("discord.notifyEvery must be greater than 0 when discord is enabled")
		}
	}

	return nil
}
