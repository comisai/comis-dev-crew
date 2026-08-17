package service

import (
	"math"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/reporter"
)

func TestRuntimeAttachmentSignedTimeReversesTwosComplementEncoding(t *testing.T) {
	cases := []int64{0, 1, -1, math.MaxInt64, math.MinInt64, math.MinInt64 + 1, -1 << 32, 1 << 32}
	for _, want := range cases {
		if got := runtimeAttachmentSignedTime(uint64(want)); got != want {
			t.Fatalf("runtimeAttachmentSignedTime(uint64(%d)) = %d, want %d", want, got, want)
		}
	}
}

func TestRuntimeAttachmentIdentityRecordRoundTripsWidestTimestamps(t *testing.T) {
	widest := reporter.RuntimeSocketIdentity{
		Device: math.MaxUint64, Inode: math.MaxUint64,
		ChangeSec: math.MaxInt64, ChangeNsec: int64(999999999),
		BirthSec: math.MaxInt64, BirthNsec: int64(999999999),
	}
	if !widest.Valid() {
		t.Fatal("widest identity is not a valid runtime socket identity")
	}
	record := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentDirectoryBound, Task: widest, Generation: widest,
	}
	record.GenerationID[0] = 9
	record.RelaySeed[0] = 7
	parsed, err := parseRuntimeAttachmentIdentityRecord(formatRuntimeAttachmentIdentityRecord(record))
	if err != nil {
		t.Fatalf("parseRuntimeAttachmentIdentityRecord() error = %v", err)
	}
	if parsed != record {
		t.Fatalf("round-tripped record = %#v, want %#v", parsed, record)
	}
}

func TestRuntimeAttachmentIdentityRecordRejectsOutOfRangeStage(t *testing.T) {
	widest := reporter.RuntimeSocketIdentity{
		Device: 1, Inode: 1, ChangeSec: 1, ChangeNsec: 0, BirthSec: 1, BirthNsec: 0,
	}
	record := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentDirectoryBound, Task: widest, Generation: widest,
	}
	record.GenerationID[0] = 9
	record.RelaySeed[0] = 7
	encoded := formatRuntimeAttachmentIdentityRecord(record)
	if _, err := parseRuntimeAttachmentIdentityRecord("zz" + encoded[2:]); err == nil {
		t.Fatal("record parser accepted an unparsable stage")
	}
}
