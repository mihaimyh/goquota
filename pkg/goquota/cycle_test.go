package goquota

import (
"testing"
"time"
)

func TestCurrentCycleForStart(t *testing.T) {
// Anniversary on the 10th
start := time.Date(2023, 1, 10, 0, 0, 0, 0, time.UTC)

tests := []struct {
name      string
now       time.Time
wantStart time.Time
wantEnd   time.Time
}{
{
name:      "In first month",
now:       time.Date(2023, 1, 15, 12, 0, 0, 0, time.UTC),
wantStart: time.Date(2023, 1, 10, 0, 0, 0, 0, time.UTC),
wantEnd:   time.Date(2023, 2, 10, 0, 0, 0, 0, time.UTC),
},
{
name:      "Exactly on boundary",
now:       time.Date(2023, 2, 10, 0, 0, 0, 0, time.UTC),
wantStart: time.Date(2023, 2, 10, 0, 0, 0, 0, time.UTC),
wantEnd:   time.Date(2023, 3, 10, 0, 0, 0, 0, time.UTC),
},
{
name:      "Crosses year boundary",
now:       time.Date(2023, 12, 25, 0, 0, 0, 0, time.UTC),
wantStart: time.Date(2023, 12, 10, 0, 0, 0, 0, time.UTC),
wantEnd:   time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC),
},
{
name:      "Future start (skew)",
now:       time.Date(2022, 12, 21, 0, 0, 0, 0, time.UTC),
wantStart: time.Date(2023, 1, 10, 0, 0, 0, 0, time.UTC),
wantEnd:   time.Date(2023, 2, 10, 0, 0, 0, 0, time.UTC),
},
}

for _, tt := range tests {
t.Run(tt.name, func(t *testing.T) {
gotS, gotE := CurrentCycleForStart(start, tt.now)
if !gotS.Equal(tt.wantStart) {
t.Errorf("start: got %v, want %v", gotS, tt.wantStart)
}
if !gotE.Equal(tt.wantEnd) {
t.Errorf("end: got %v, want %v", gotE, tt.wantEnd)
}
})
}
}

func TestCurrentCycleForStart_MonthEnd(t *testing.T) {
// Anniversary on the 31st
start := time.Date(2023, 1, 31, 0, 0, 0, 0, time.UTC)

tests := []struct {
name      string
now       time.Time
wantStart time.Time
wantEnd   time.Time
}{
{
name:      "February (28 days)",
now:       time.Date(2023, 2, 15, 0, 0, 0, 0, time.UTC),
wantStart: time.Date(2023, 1, 31, 0, 0, 0, 0, time.UTC),
wantEnd:   time.Date(2023, 2, 28, 0, 0, 0, 0, time.UTC),
},
{
name:      "March from February",
now:       time.Date(2023, 3, 15, 0, 0, 0, 0, time.UTC),
wantStart: time.Date(2023, 2, 28, 0, 0, 0, 0, time.UTC),
wantEnd:   time.Date(2023, 3, 31, 0, 0, 0, 0, time.UTC),
},
{
name:      "Leap year (Feb 2024)",
now:       time.Date(2024, 2, 15, 0, 0, 0, 0, time.UTC),
wantStart: time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC),
wantEnd:   time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC),
},
}

for _, tt := range tests {
t.Run(tt.name, func(t *testing.T) {
gotS, gotE := CurrentCycleForStart(start, tt.now)
if !gotS.Equal(tt.wantStart) {
t.Errorf("start: got %v, want %v", gotS, tt.wantStart)
}
if !gotE.Equal(tt.wantEnd) {
t.Errorf("end: got %v, want %v", gotE, tt.wantEnd)
}
})
}
}

func TestAddMonthsSafe(t *testing.T) {
tests := []struct {
name   string
base   time.Time
months int
want   time.Time
}{
{
name:   "Jan 31 + 1 month = Feb 28",
base:   time.Date(2023, 1, 31, 0, 0, 0, 0, time.UTC),
months: 1,
want:   time.Date(2023, 2, 28, 0, 0, 0, 0, time.UTC),
},
{
name:   "Jan 31 + 1 month (Leap) = Feb 29",
base:   time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC),
months: 1,
want:   time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC),
},
{
name:   "Mar 31 + 1 month = Apr 30",
base:   time.Date(2023, 3, 31, 0, 0, 0, 0, time.UTC),
months: 1,
want:   time.Date(2023, 4, 30, 0, 0, 0, 0, time.UTC),
},
{
name:   "Aug 31 + 6 months = Feb 28",
base:   time.Date(2022, 8, 31, 0, 0, 0, 0, time.UTC),
months: 6,
want:   time.Date(2023, 2, 28, 0, 0, 0, 0, time.UTC),
},
}

for _, tt := range tests {
t.Run(tt.name, func(t *testing.T) {
got := addMonthsSafe(tt.base, tt.months)
if !got.Equal(tt.want) {
t.Errorf("got %v, want %v", got, tt.want)
}
})
}
}
