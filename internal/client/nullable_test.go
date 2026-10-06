package client

import (
	"encoding/json"
	"testing"
	"time"
)

type payload struct {
	DueDate NullTime `json:"due_date"`
}

func TestMarshalZeroIsNull(t *testing.T) {
	p := payload{DueDate: NullTime{}}
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"due_date":null}` {
		t.Errorf("got %s, want zero → null", out)
	}
}

func TestMarshalValid(t *testing.T) {
	ts := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	p := payload{DueDate: NewNullTime(ts)}
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"due_date":"2026-10-05T12:00:00Z"}` {
		t.Errorf("got %s", out)
	}
}

func TestUnmarshalVikunjaZero(t *testing.T) {
	var p payload
	if err := json.Unmarshal([]byte(`{"due_date":"0001-01-01T00:00:00Z"}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.DueDate.Valid {
		t.Error("zero time parsed as valid")
	}
	if !p.DueDate.Time.IsZero() {
		t.Errorf("time = %v, want zero", p.DueDate.Time)
	}
}

func TestUnmarshalNull(t *testing.T) {
	var p payload
	if err := json.Unmarshal([]byte(`{"due_date":null}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.DueDate.Valid {
		t.Error("null parsed as valid")
	}
}

func TestUnmarshalEmptyString(t *testing.T) {
	var p payload
	if err := json.Unmarshal([]byte(`{"due_date":""}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.DueDate.Valid {
		t.Error("empty string parsed as valid")
	}
}

func TestUnmarshalValid(t *testing.T) {
	var p payload
	if err := json.Unmarshal([]byte(`{"due_date":"2026-10-05T12:00:00Z"}`), &p); err != nil {
		t.Fatal(err)
	}
	if !p.DueDate.Valid {
		t.Fatal("valid date lost")
	}
	want := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	if !p.DueDate.Time.Equal(want) {
		t.Errorf("got %v, want %v", p.DueDate.Time, want)
	}
}

func TestNoFakeDatesInRoundtrip(t *testing.T) {
	in := []byte(`{"due_date":"0001-01-01T00:00:00Z","start_date":"2026-03-01T09:30:00Z"}`)
	type two struct {
		DueDate   NullTime `json:"due_date"`
		StartDate NullTime `json:"start_date"`
	}
	var p two
	if err := json.Unmarshal(in, &p); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if s != `{"due_date":null,"start_date":"2026-03-01T09:30:00Z"}` {
		t.Errorf("roundtrip = %s, want null + real date", s)
	}
}

func TestValueOr(t *testing.T) {
	def := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := (NullTime{}).ValueOr(def); !got.Equal(def) {
		t.Errorf("invalid → %v, want default", got)
	}
	valid := NewNullTime(time.Date(2027, 5, 5, 0, 0, 0, 0, time.UTC))
	if got := valid.ValueOr(def); got != valid.Time {
		t.Errorf("valid → %v, want %v", got, valid.Time)
	}
}

func TestString(t *testing.T) {
	if got := (NullTime{}).String(); got != "not set" {
		t.Errorf("got %q, want %q", got, "not set")
	}
	valid := NewNullTime(time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC))
	if got := valid.String(); got != "2026-10-05T12:00:00Z" {
		t.Errorf("got %q", got)
	}
}
