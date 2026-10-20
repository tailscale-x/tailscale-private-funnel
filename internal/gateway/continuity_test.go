package gateway

import (
	"testing"
	"time"
)

func TestContinuityIsBoundedAndCannotCreateUnreachableHost(t *testing.T) {
	c := &BackendContinuity{}
	now := time.Now()
	if c.Keep(false, false, now) {
		t.Fatal("unreachable new host admitted")
	}
	if !c.Keep(true, false, now) || !c.Keep(true, false, now.Add(BackendContinuityGrace-time.Second)) {
		t.Fatal("existing host withdrew before grace")
	}
	if c.Keep(true, false, now.Add(BackendContinuityGrace)) {
		t.Fatal("unreachable host retained forever")
	}
	if !c.Keep(true, true, now.Add(3*time.Minute)) || !c.Keep(true, false, now.Add(4*time.Minute)) {
		t.Fatal("recovery did not reset grace")
	}
}
