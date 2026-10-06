package client

import (
	"encoding/json"
	"time"
)

type NullTime struct {
	time.Time
	Valid bool
}

func NewNullTime(t time.Time) NullTime {
	return NullTime{Time: t, Valid: !t.IsZero()}
}

func (nt NullTime) MarshalJSON() ([]byte, error) {
	if !nt.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(nt.Time.Format(time.RFC3339))
}

func (nt *NullTime) UnmarshalJSON(data []byte) error {
	nt.Valid = false
	nt.Time = time.Time{}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		if string(data) == "null" {
			return nil
		}
		return err
	}
	if s == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return err
	}
	if parsed.IsZero() {
		return nil
	}
	nt.Time = parsed
	nt.Valid = true
	return nil
}

func (nt NullTime) ValueOr(def time.Time) time.Time {
	if nt.Valid {
		return nt.Time
	}
	return def
}

func (nt NullTime) String() string {
	if !nt.Valid {
		return "not set"
	}
	return nt.Time.Format(time.RFC3339)
}
