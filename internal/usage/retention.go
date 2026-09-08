package usage

import (
	"strconv"
	"time"
)

const DefaultRetention = "13-months"

// Retention is independent of diagnostics and sanitized checkpoint expiry.
// Zero Days uses thirteen calendar months; Unlimited disables detail expiry.
type Retention struct {
	Days      int  `json:"days"`
	Unlimited bool `json:"unlimited"`
}

func ParseRetention(setting string) (Retention, error) {
	switch setting {
	case DefaultRetention:
		return Retention{}, nil
	case "unlimited":
		return Retention{Unlimited: true}, nil
	}
	days, err := strconv.Atoi(setting)
	// Dates outside the supported Gregorian timestamp range cannot expire data.
	if err != nil || days < 30 || days > 3652059 {
		return Retention{}, ErrInvalid
	}
	return Retention{Days: days}, nil
}

func (policy Retention) String() string {
	if policy.Unlimited {
		return "unlimited"
	}
	if policy.Days == 0 {
		return DefaultRetention
	}
	return strconv.Itoa(policy.Days)
}

func (policy Retention) Cutoff(now time.Time) *time.Time {
	if policy.Unlimited {
		return nil
	}
	now = now.UTC()
	var cutoff time.Time
	if policy.Days != 0 {
		cutoff = now.AddDate(0, 0, -policy.Days)
	} else {
		month := time.Date(now.Year(), now.Month()-13, 1, now.Hour(), now.Minute(), now.Second(), now.Nanosecond(), time.UTC)
		day := min(now.Day(), month.AddDate(0, 1, -1).Day())
		cutoff = month.AddDate(0, 0, day-1)
	}
	return &cutoff
}
