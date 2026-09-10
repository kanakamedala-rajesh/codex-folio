package continuation

import (
	"strconv"
	"time"
)

const (
	DefaultRepositoryRetention = "30"
	DefaultTranscriptRetention = "7"
)

type RetentionPolicy struct {
	RepositoryFirst    string `json:"repository_first"`
	TranscriptAssisted string `json:"transcript_assisted"`
}

func ParseRetention(setting string) (days int, unlimited bool, err error) {
	if setting == "unlimited" {
		return 0, true, nil
	}
	days, err = strconv.Atoi(setting)
	if err != nil || days < 1 || days > 3652059 {
		return 0, false, ErrCheckpointInvalid
	}
	return days, false, nil
}

func (policy RetentionPolicy) setting(source string) (string, error) {
	setting := policy.RepositoryFirst
	defaultSetting := DefaultRepositoryRetention
	if source == SourceTranscriptAssisted {
		setting, defaultSetting = policy.TranscriptAssisted, DefaultTranscriptRetention
	} else if source != SourceRepositoryFirst {
		return "", ErrCheckpointInvalid
	}
	if setting == "" {
		setting = defaultSetting
	}
	if _, _, err := ParseRetention(setting); err != nil {
		return "", err
	}
	return setting, nil
}

func retentionExpiry(createdAt time.Time, setting string) (*time.Time, error) {
	days, unlimited, err := ParseRetention(setting)
	if err != nil || unlimited {
		return nil, err
	}
	expiresAt := createdAt.UTC().AddDate(0, 0, days)
	if expiresAt.Year() > 9999 {
		return nil, ErrCheckpointInvalid
	}
	return &expiresAt, nil
}
